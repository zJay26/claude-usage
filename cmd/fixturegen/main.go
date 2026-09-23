package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", "", "fixture root")
	gib := flag.Int64("gib", 10, "irrelevant JSON record size in GiB")
	flag.Parse()
	if *root == "" || *gib <= 0 {
		fmt.Fprintln(os.Stderr, "usage: fixturegen -root PATH [-gib 10]")
		os.Exit(2)
	}
	sessionDir := filepath.Join(*root, ".claude", "projects", "synthetic")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		panic(err)
	}
	path := filepath.Join(sessionDir, "large-fixture.jsonl")
	file, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	writer := bufio.NewWriterSize(file, 1<<20)
	fmt.Fprintln(writer, `{"type":"custom-title","sessionId":"memory-fixture","customTitle":"Synthetic large record"}`)
	writer.WriteString(`{"type":"user","message":{"content":"`)
	remaining := *gib * (1 << 30)
	chunk := make([]byte, 1<<20)
	for i := range chunk {
		chunk[i] = 'x'
	}
	for remaining > 0 {
		n := int64(len(chunk))
		if n > remaining {
			n = remaining
		}
		if _, err := writer.Write(chunk[:n]); err != nil {
			panic(err)
		}
		remaining -= n
	}
	writer.WriteString(`"}}` + "\n")
	fmt.Fprintln(writer, `{"timestamp":"2026-07-30T00:00:02Z","type":"assistant","sessionId":"memory-fixture","requestId":"req-memory","message":{"id":"msg-memory","model":"claude-opus-5-5","usage":{"input_tokens":60,"cache_read_input_tokens":20,"output_tokens":20}}}`)
	if err := writer.Flush(); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
	fmt.Println(path)
}
