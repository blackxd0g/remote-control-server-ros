//go:build ignore

package main

import (
	"archive/tar"
	"io"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: make-api-layer <binary> <layer.tar>")
	}
	input, err := os.Open(os.Args[1])
	if err != nil { log.Fatal(err) }
	defer input.Close()
	info, err := input.Stat()
	if err != nil { log.Fatal(err) }
	output, err := os.Create(os.Args[2])
	if err != nil { log.Fatal(err) }
	writer := tar.NewWriter(output)
	header := &tar.Header{Name: "usr/local/bin/art-api", Mode: 0o755, Size: info.Size(), Typeflag: tar.TypeReg, Uid: 0, Gid: 0}
	if err = writer.WriteHeader(header); err != nil { log.Fatal(err) }
	if _, err = io.Copy(writer, input); err != nil { log.Fatal(err) }
	if err = writer.Close(); err != nil { log.Fatal(err) }
	if err = output.Close(); err != nil { log.Fatal(err) }
}
