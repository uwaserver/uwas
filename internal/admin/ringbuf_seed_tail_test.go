package admin

import (
	"fmt"
	"reflect"
	"testing"
)

func TestRingBufferSeedRetainsNewest(t *testing.T) {
	for _, capacity := range []int{0, 1, 3, 5} {
		for n := 0; n < 12; n++ {
			r := newRingBuffer[int](capacity)
			values := make([]int, n)
			for i := range values {
				values[i] = i + 1
			}
			r.Seed(values)
			start := max(0, n-r.cap)
			want := append([]int{}, values[start:]...)
			if got := r.Snapshot(); !reflect.DeepEqual(got, want) {
				t.Fatalf("cap=%d n=%d got=%v want=%v", capacity, n, got, want)
			}
			_, snapshot := r.PosAndEntries()
			if !reflect.DeepEqual(snapshot, want) {
				t.Fatal("position snapshot")
			}
			if n > 0 {
				values[n-1] = 99
			}
			if !reflect.DeepEqual(r.Snapshot(), want) {
				t.Fatal("seed slice alias")
			}
			r.Append(20)
			want = append(want, 20)
			if len(want) > r.cap {
				want = want[len(want)-r.cap:]
			}
			if !reflect.DeepEqual(r.Snapshot(), want) {
				t.Fatal("append after seed")
			}
			r.Seed(nil)
			if len(r.Snapshot()) != 0 || r.full || r.pos != 0 {
				t.Fatal("repeat clear")
			}
		}
	}
	fmt.Println("FIX VERIFIED")
}
