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
