package main

import (
	"compress/gzip"
	"flag"
	"io"
	"os"
)

func main() {
	decompress := flag.Bool("d", false, "decompress the input")
	flag.Parse()

	var err error
	if *decompress {
		err = decompressData()
	} else {
		err = compressData()
	}
	if err != nil {
		panic(err)
	}
}

func compressData() error {
	gw := gzip.NewWriter(os.Stdout)
	defer gw.Close()

	_, err := io.Copy(gw, os.Stdin)
	return err
}

func decompressData() error {
	gr, err := gzip.NewReader(os.Stdin)
	if err != nil {
		return err
	}
	defer gr.Close()

	_, err = io.Copy(os.Stdout, gr)
	return err
}
