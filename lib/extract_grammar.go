//go:build ignore

package main

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

// ExtractEBNF reads a Go source file, extracts its package documentation,
// and extracts out the EBNF grammar relying on the "//\t" prefix and " ." suffix.
func ExtractEBNF(filename string) (string, error) {
	fset := token.NewFileSet()
	// Parse only the package clause and comments to minimize overhead
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments|parser.PackageClauseOnly)
	if err != nil {
		return "", err
	}

	if f.Doc == nil {
		return "", fmt.Errorf("no package documentation found in %s", filename)
	}

	var ebnf, block []string

	inBlock := false

	// f.Doc.List contains the raw comments, preserving the "//" and whitespace
	for _, comment := range f.Doc.List {
		s := comment.Text
		hasPrefix := strings.HasPrefix(s, "//\t")
		if hasPrefix {
			s = s[3:]
			s = strings.TrimRight(s, "\t ")
			if strings.HasPrefix(s, "#") {
				continue
			}
		}

		switch {
		case inBlock:
			switch {
			case hasPrefix:
				block = append(block, s)
			default:
				inBlock = false
				w := 0
				for _, v := range block {
					if v != "" {
						block[w] = v
						w++
					}
				}
				if w == 0 {
					break
				}

				block = block[:w]
				if !strings.HasSuffix(block[w-1], " .") {
					break
				}

				ebnf = append(ebnf, block...)
			}
		default:
			switch {
			case hasPrefix:
				block = block[:0]
				block = append(block, s)
				inBlock = true
			}
		}
	}

	return strings.Join(ebnf, "\n") + "\n", nil
}

func main() {
	// Parse CLI flags (this automatically handles and consumes '--')
	flag.Parse()

	// flag.NArg() gets the number of arguments left after flags and '--' are processed
	if flag.NArg() < 1 {
		fmt.Println("Usage: go run extract_grammar.go -- <filename>")
		os.Exit(1)
	}

	// flag.Arg(0) safely gets the first actual argument (e.g., "../doc.go")
	grammar, err := ExtractEBNF(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(grammar)
}
