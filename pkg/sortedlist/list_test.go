package sortedlist

import "testing"

func TestListNew(t *testing.T) {
	l := New()
	n := l.First()
	if n != nil {
		t.Error("First item of list should be nil.")
	}

	n = l.head.Next()
	if n != nil {
		t.Error("Next element of empty list should be nil.")
	}
}
