// Command fakeconsole serves a fake /agent/v1 API over TLS for trying the
// agent locally. It prints every request body it receives.
//
//	go run ./tools/fakeconsole -addr 127.0.0.1:8443 -ca-out /tmp/x/ca.pem
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"

	"github.com/marcpope/cortendesk-agent/internal/fakeconsole"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8443", "listen address")
	token := flag.String("token", "cde_dev", "enrollment token to accept")
	interval := flag.Int("interval", 60, "report interval to hand out, seconds")
	caOut := flag.String("ca-out", "", "write the server certificate (PEM) here")
	watch := flag.String("watch", "", "comma-separated watch_services to hand out")
	flag.Parse()

	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	c := fakeconsole.NewOn(l, *token, *interval, true)
	defer c.Close()
	if *watch != "" {
		c.SetWatch(strings.Split(*watch, ","))
	}
	c.OnRequest = func(r fakeconsole.Request) {
		var buf bytes.Buffer
		if json.Indent(&buf, r.Body, "", "  ") != nil {
			buf.Write(r.Body)
		}
		fmt.Printf("--- %s -> %d\n%s\n", r.Path, r.Status, buf.String())
	}
	if *caOut != "" {
		if err := os.WriteFile(*caOut, c.CAPEM(), 0o600); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("fake console on %s, token %s\n", c.URL(), *token)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}
