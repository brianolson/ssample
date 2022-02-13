package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
)

type Collector struct {
	LinesToKeep int

	lines       []string
	lineNumbers []int
	// TODO: also add lineTimes []time.Time ?
	linesSeen int

	rng *rand.Rand

	l sync.Mutex
}

// AddLine maybe adds the line
func (c *Collector) AddLine(line string) {
	c.l.Lock()
	defer c.l.Unlock()
	if c.rng == nil {
		c.rng = rand.New(rand.NewSource(rand.Int63()))
	}
	keep := len(c.lines) < c.LinesToKeep
	if !keep {
		//if c.rng.Float64() < 1/c.linesSeen {
		keep = (c.rng.Float64() * float64(c.linesSeen)) < 1
	}
	if keep {
		if len(c.lines) < c.LinesToKeep {
			c.lines = append(c.lines, line)
			c.lineNumbers = append(c.lineNumbers, c.linesSeen)
		} else {
			evict := c.rng.Intn(len(c.lines))
			c.lines[evict] = line
			c.lineNumbers[evict] = c.linesSeen
		}
	}

	c.linesSeen++
}

// LinesUnordered returns a copy of the collected lines
func (c *Collector) LinesUnordered() []string {
	c.l.Lock()
	defer c.l.Unlock()
	out := make([]string, len(c.lines))
	copy(out, c.lines)
	return out
}

// LinesAndNumbers returns a sorted copy of the collect lines and their line numbers
func (c *Collector) LinesAndNumbers() (lines []string, lineNumbers []int) {
	s := sorter{}
	c.l.Lock()
	s.lines = make([]string, len(c.lines))
	copy(s.lines, c.lines)
	s.lineNumbers = make([]int, len(c.lineNumbers))
	copy(s.lineNumbers, c.lineNumbers)
	c.l.Unlock()
	sort.Sort(&s)
	return s.lines, s.lineNumbers
}

type sorter struct {
	lines       []string
	lineNumbers []int
}

func (s sorter) Len() int {
	return len(s.lines)
}

func (s sorter) Less(i, j int) bool {
	return s.lineNumbers[i] < s.lineNumbers[j]
}

func (s sorter) Swap(i, j int) {
	tl := s.lines[i]
	tn := s.lineNumbers[i]
	s.lines[i] = s.lines[j]
	s.lineNumbers[i] = s.lineNumbers[j]
	s.lines[j] = tl
	s.lineNumbers[j] = tn
}

var shouldquit uint32
var globalm sync.Mutex
var gcond *sync.Cond

func init() {
	gcond = sync.NewCond(&globalm)
}

func gogently(c chan os.Signal) {
	xs := <-c
	fmt.Fprintf(os.Stderr, "got signal: %v\n", xs)
	atomic.StoreUint32(&shouldquit, 1)
	gcond.Broadcast()
}

func reader(c *Collector) {
	defer gcond.Broadcast()
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		xs := atomic.LoadUint32(&shouldquit)
		if xs != 0 {
			fmt.Fprintf(os.Stderr, "got interrupt\n")
			return
		}
		c.AddLine(in.Text())
	}
	fmt.Fprintf(os.Stderr, "stdin exhausted: %v", in.Err())
}

type ssampleServer struct {
	c *Collector
}

type LineNoResponse struct {
	Lines       []string `json:"lines"`
	LineNumbers []int    `json:"lineNumbers"`
}

func (s *ssampleServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var out LineNoResponse
	out.Lines, out.LineNumbers = s.c.LinesAndNumbers()
	blob, err := json.Marshal(out)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(500)
		fmt.Fprintf(w, "json err: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(blob)
}

func main() {
	var c Collector

	var haddr string
	flag.StringVar(&haddr, "http", "", "host:port (or :port) to serve http on")
	flag.IntVar(&c.LinesToKeep, "l", 100, "keep this many lines, uniformly sampled across all input")
	flag.Parse()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	go gogently(sigs)
	go reader(&c)
	if haddr != "" {
		server := ssampleServer{&c}
		hs := http.Server{
			Addr:    haddr,
			Handler: &server,
		}
		go hs.ListenAndServe()
	}
	globalm.Lock()
	gcond.Wait()
	globalm.Unlock()
	lines, nos := c.LinesAndNumbers()
	for i, ln := range nos {
		fmt.Printf("%d\t%s\n", ln, string(lines[i]))
	}
}
