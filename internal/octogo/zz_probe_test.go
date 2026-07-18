package octogo

import (
	"fmt"
	"testing"
	"testing/fstest"
)

func TestZZProbe(t *testing.T) {
	for _, c := range []string{"x %= 3", "x &^= 2", "x += 1", "x <<= 1"} {
		src := "func main() {\n\tx := 7\n\t" + c + "\n\tprintln(x)\n}\n"
		fsys := fstest.MapFS{"main.ogo": &fstest.MapFile{Data: []byte(src)}}
		_, err := Build(-1, []string{"main.ogo"}, fsys)
		st := "accepted"
		if err != nil {
			st = fmt.Sprintf("rejected: %v", err)
		}
		fmt.Printf("  %-10s %s\n", c, st)
	}
}
