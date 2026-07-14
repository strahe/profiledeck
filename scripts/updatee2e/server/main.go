package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
)

func main() {
	root := flag.String("root", "", "directory to serve")
	addressFile := flag.String("address-file", "", "file that receives the listener URL")
	flag.Parse()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(*addressFile, []byte("http://"+listener.Addr().String()), 0o600); err != nil {
		panic(err)
	}
	fmt.Println(listener.Addr().String())
	if err := http.Serve(listener, http.FileServer(http.Dir(*root))); err != nil {
		panic(err)
	}
}
