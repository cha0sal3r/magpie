package handoff

import (
	"testing"

	"github.com/cha0sal3r/magpie-friday/internal/testenv"
)

// The tests run in a home of their own, never the user's real files.
func TestMain(m *testing.M) { testenv.Main(m) }
