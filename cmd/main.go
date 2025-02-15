package main

import (
	"flag"

	iparser "github.com/dxldb/minidemo-encoder/internal/parser"
)

func readArgs() string {
	var filepath string
	flag.StringVar(&filepath, "file", "", "demo file path")
	flag.Parse()
	return filepath
}

func main() {
	iparser.Start("123.dem")
}
