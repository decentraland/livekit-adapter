package main

import (
	"fmt"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/net/websocket"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	url := "ws://localhost:12345/ws"
	ws, err := websocket.Dial(url, "transport", "")
	if err != nil {
		log.Err(err)
		log.Fatal()
	}

	if _, err := ws.Write([]byte("hello, world!\n")); err != nil {
		log.Err(err)
	}

	var msg = make([]byte, 512)
	var n int
	if n, err = ws.Read(msg); err != nil {
		log.Err(err)
		log.Fatal()
	}
	fmt.Printf("Received: %s.\n", msg[:n])
}
