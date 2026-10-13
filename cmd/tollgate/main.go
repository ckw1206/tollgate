package main

import (
	"log"

	"tollgate/core/config"
)

func main() {
	if _, err := config.Load(); err != nil {
		log.Fatal(err)
	}
	log.Println("config ok")
}
