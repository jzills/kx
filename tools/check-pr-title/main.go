// Command check-pr-title refuses a pull request title that can't become
// release copy (relnotes.TitleProblems).
//
//	go run ./tools/check-pr-title "fix(get): the -o name listing prints kubectl's reply"
//
// Each argument is a title. pr-title.yml passes the PR's title, which is
// what a squash merge into develop keeps as the commit subject.
package main

import (
	"fmt"
	"os"

	"github.com/jzills/kx/tools/internal/relnotes"
)

func main() {
	failed := false
	for _, title := range os.Args[1:] {
		for _, problem := range relnotes.TitleProblems(title) {
			fmt.Fprintf(os.Stderr, "%q: %s\n", title, problem)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
