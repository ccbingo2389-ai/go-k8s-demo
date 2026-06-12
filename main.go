package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from Go App on 2026061207")
	})
	fmt.Println("Server starting on port 8080...")
	http.ListenAndServe(":8080", nil)
}
