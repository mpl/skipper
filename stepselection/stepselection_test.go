package stepselection

import (
	"bytes"
	"testing"
)

func TestWalkUpStepTree(t *testing.T) {

	stepTree := []string{"p1", "p2", "p3"}

	want := "[\"p1\"]\n[\"p1\",\"p2\"]\n[\"p1\",\"p2\",\"p3\"]\n"

	got := new(bytes.Buffer)
	walkUpStepTree(stepTree, func(cmdTree CmdTree) {
		got.WriteString(cmdTree.Name() + "\n")
	})
	gotString := got.String()

	if gotString != want {
		t.Errorf("got %q wanted %q", gotString, want)
	}
}
