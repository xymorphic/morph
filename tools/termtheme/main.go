package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/xymorphic/morph/pkg/termtheme"
)

func main() {
	timeout := flag.Duration("timeout", 300*time.Millisecond, "terminal response timeout")
	asJSON := flag.Bool("json", false, "print JSON output")
	flag.Parse()

	res := termtheme.Detect(*timeout)
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(res)
		if res.Error != "" {
			os.Exit(1)
		}
		return
	}

	if res.Error != "" {
		fmt.Fprintln(os.Stderr, res.Error)
		os.Exit(1)
	}

	if res.Background == "" {
		fmt.Println(res.Theme)
		return
	}

	fmt.Printf("%s %s\n", res.Theme, res.Background)
}
