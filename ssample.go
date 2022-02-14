package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"time"
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
		c.rng = rand.New(rand.NewSource(time.Now().Unix()))
	}
	if len(c.lines) < c.LinesToKeep {
		c.lines = append(c.lines, line)
		c.lineNumbers = append(c.lineNumbers, c.linesSeen)
	} else {
		rf := c.rng.Float64()
		keep := rf < (float64(c.LinesToKeep-1) / float64(c.linesSeen))
		if keep {
			evict := c.rng.Intn(len(c.lines))
			c.lines[evict] = line
			c.lineNumbers[evict] = c.linesSeen
		}
	}

	c.linesSeen++
}

func (c *Collector) Seen() int {
	c.l.Lock()
	defer c.l.Unlock()
	return c.linesSeen
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

func reader(c *Collector, tee io.Writer, echo bool) {
	defer func() {
		if tee != nil {
			wc, ok := tee.(io.WriteCloser)
			if ok {
				wc.Close()
			}
		}
		gcond.Broadcast()
	}()
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		xs := atomic.LoadUint32(&shouldquit)
		if xs != 0 {
			fmt.Fprintf(os.Stderr, "got interrupt\n")
			return
		}
		line := in.Text()
		if tee != nil {
			fmt.Fprintf(tee, "%s\n", line)
		}
		if echo {
			fmt.Fprintf(os.Stdout, "%s\n", line)
		}
		c.AddLine(line)
	}
	fmt.Fprintf(os.Stderr, "stdin exhausted: %v", in.Err())
}

var falseish []string = []string{"", "f", "F", "False", "FALSE", "false", "0"}

func boolish(s string) bool {
	for _, v := range falseish {
		if v == s {
			return false
		}
	}
	return true
}

type ssampleServer struct {
	c *Collector
}

type LineNoResponse struct {
	Lines       []string `json:"lines"`
	LineNumbers []int    `json:"lineNumbers"`
	LinesSeen   int      `json:"seen"`
}

func (s *ssampleServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	textmode := boolish(r.FormValue("t"))
	plainmode := boolish(r.FormValue("p"))
	var out LineNoResponse
	out.Lines, out.LineNumbers = s.c.LinesAndNumbers()
	out.LinesSeen = s.c.Seen()
	if plainmode {
		for _, line := range out.Lines {
			fmt.Fprintf(w, "%s\n", line)
		}
	} else if textmode {
		for i, ln := range out.LineNumbers {
			fmt.Fprintf(w, "%d\t%s\n", ln, out.Lines[i])
		}
	} else {
		// json
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
}

func maybefail(err error, xf string, args ...interface{}) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, xf, args...)
	os.Exit(1)
}

func main() {
	var c Collector
	var teef io.Writer

	var haddr string
	var tee string
	var teez string
	var echo bool
	flag.StringVar(&haddr, "http", "", "host:port (or :port) to serve http on")
	flag.IntVar(&c.LinesToKeep, "l", 100, "keep this many lines, uniformly sampled across all input")
	flag.StringVar(&tee, "a", "", "also append all input to file")
	flag.StringVar(&teez, "teez", "", "also write all input to file (gzipped)")
	flag.BoolVar(&echo, "echo", false, "also write all lines to stdout as they happen")
	flag.Parse()

	var err error
	if tee != "" {
		teef, err = os.OpenFile(tee, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		maybefail(err, "%s: %v\n", tee, err)
	} else if teez != "" {
		// sadly gzip doesn't append
		rawf, err := os.OpenFile(teez, os.O_CREATE|os.O_WRONLY, 0644)
		maybefail(err, "%s: %v\n", teez, err)
		defer rawf.Close()
		teef = gzip.NewWriter(rawf)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	go gogently(sigs)
	go reader(&c, teef, echo)
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
		fmt.Printf("%d\t%s\n", ln, lines[i])
	}
}
