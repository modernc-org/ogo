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
// and strictly parses out the EBNF grammar relying on the "//\t" prefix.
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

	var ebnfBuilder strings.Builder

	// f.Doc.List contains the raw comments, preserving the "//" and whitespace
	for _, comment := range f.Doc.List {
		rawText := comment.Text

		// Check for the strict preformatted prefix
		if strings.HasPrefix(rawText, "//\t") {
			// Strip the "//\t" prefix (length of 3)
			payload := rawText[3:]

			// Now we can safely trim spaces to check for egg-specific comments
			trimmed := strings.TrimSpace(payload)

			// Ignore empty lines or egg grammar comments (starting with '#')
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}

			// Write the extracted payload. We append a newline because
			// the raw ast.Comment.Text does not include the trailing newline.
			ebnfBuilder.WriteString(payload + "\n")
		}
	}

	return ebnfBuilder.String(), nil
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
