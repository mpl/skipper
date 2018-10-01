package cmd

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestChildSkipperArgs(t *testing.T) {

	args := []string{"./skipper", "--", "foo"}
	got := childSkipperArgs("bid", args)

	want := []string{"./skipper", "--id", "bid", "--", "foo"}
	if diff := cmp.Diff(got, want); len(diff) > 0 {
		fmt.Print(diff)
		t.Errorf("Unexpected skipper children args. Got %q, wanted %q", got, want)
	}

}
