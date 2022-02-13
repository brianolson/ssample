# ssample
Streaming Sample

Command line tool reads from stdin, keeps a uniform sample of N lines. On ^C those lines are printed to stdout. Optionally can serve current sample of N lines by http.

```sh
noisyprocess -foo -bar -baz| ssample -l 10 -http :4422
```

Get the latest sample by curl:

```sh
# fetch json {"lines":[...], "lineNumbers":[...]}
curl 'localhost:4422'
# fetch text "{lineNumber}\t{line}\n"
curl 'localhost:4422/?t=1'
# fetch plain lines "{line}\n"
curl 'localhost:4422/?p=1'
```

## Usage

```
$ ./ssample --help
Usage of ./ssample:
  -a string
    	also append all input to file
  -echo
    	also write all lines to stdout as they happen
  -http string
    	host:port (or :port) to serve http on
  -l int
    	keep this many lines, uniformly sampled across all input (default 100)
  -teez string
    	also write all input to file (gzipped)
```

## Install

```sh
go install github.com/brianolson/ssample@latest
```
