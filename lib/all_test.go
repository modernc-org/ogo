package octogo // import "octogo.dec/octogo/lib"

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestTODO(t *testing.T) {
	t.Log("TODO")
}
