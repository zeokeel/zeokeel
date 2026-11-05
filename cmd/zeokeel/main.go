// Command zeokeel is ZeoKeel: a pure-Go desktop AI agent built with mygo's
// native UI. Its visual system is internal/brand + internal/design.
package main

import (
	"errors"
	"log"

	"github.com/zeokeel/zeokeel/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		if errors.Is(err, app.ErrAlreadyRunning) {
			log.Println("ZeoKeel is already running")
			return
		}
		log.Fatal(err)
	}
}
