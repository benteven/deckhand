package tasks

import "testing"

func TestSort(t *testing.T) {
	ts := []Task{
		{ID: "a", Status: "needs-you", Prio: "low"},
		{ID: "b", Status: "working", Prio: "urgent"},
		{ID: "c", Status: "needs-you", Prio: "urgent"},
		{ID: "d", Status: "waiting", Prio: "high"},
		{ID: "e", Status: "needs-you", Prio: "normal", Deadline: "2026-11-20"},
		{ID: "f", Status: "needs-you", Prio: "normal", Deadline: "2026-11-14"},
		{ID: "g", Status: "needs-you"},
	}
	Sort(ts)
	var got string
	for _, x := range ts {
		got += x.ID
	}
	if want := "cfegabd"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
}
