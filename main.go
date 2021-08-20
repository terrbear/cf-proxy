package main

import (
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/terrbear/cf-proxy/internal/env"
	"github.com/terrbear/cf-proxy/pkg/stack"
)

func main() {
	log.SetLevel(log.DebugLevel)

	mgr := stack.NewManager(stack.ManagerParams{
		SlackToken:             env.SlackToken(),
		CloudformationEndpoint: env.CloudformationEndpoint(),
		SlackChannel:           env.SlackChannel(),
		SlackHeader:            env.SlackHeader(),
	})

	go func() {
		t := time.NewTicker(15 * time.Second)
		for {
			<-t.C
			mgr.Broadcast()
		}
	}()

	log.Info("starting up cf proxy")

	server := &http.Server{
		Addr:           ":8442",
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		MaxHeaderBytes: 1 << 20,
		Handler:        http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mgr.HandleHTTP(w, r) }),
	}
	log.Fatal(server.ListenAndServe())
}
