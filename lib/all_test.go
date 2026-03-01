package octogo // import "octogo.dev/octogo/lib"

import (
	"os"
	"testing"

	_ "modernc.org/ccgo/v4/lib" // generator.go
	_ "modernc.org/gc/v3"       // generator.go
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestTODO(t *testing.T) {
	t.Log("TODO")
}
