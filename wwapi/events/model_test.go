package events

import (
	"math/rand/v2"
	"testing"
)

// checkDropOldestModel runs ops against a real bus and a simple model of
// two drop-oldest subscriptions (one per buffer size) and one Last. Each
// op byte: low 2 bits pick emit / read from A / read from B / check Last;
// the rest is a payload. The model is a slice that keeps the newest cap
// items, which is exactly drop-oldest.
func checkDropOldestModel(t *testing.T, capA, capB int, ops []byte) {
	t.Helper()
	b := New(Options{})
	chA, offA := Chan[tick](b, capA)
	defer offA()
	chB, offB := Chan[Event](b, capB)
	defer offB()

	var modelA, modelB []int // payload N in queue order
	var dropped uint64
	lastN, haveLast := 0, false
	push := func(q *[]int, n, c int) {
		*q = append(*q, n)
		if len(*q) > c {
			*q = (*q)[1:]
			dropped++
		}
	}
	for i, op := range ops {
		n := i // unique payload, so order mistakes show
		switch op & 3 {
		case 0:
			if err := b.Emit(tick{N: n}); err != nil {
				t.Fatal(err)
			}
			push(&modelA, n, capA)
			push(&modelB, n, capB)
			lastN, haveLast = n, true
		case 1:
			if len(modelA) == 0 {
				if len(chA) != 0 {
					t.Fatalf("op %d: A has %d, model empty", i, len(chA))
				}
				continue
			}
			if e := <-chA; e.N != modelA[0] {
				t.Fatalf("op %d: A got %d, model %d", i, e.N, modelA[0])
			}
			modelA = modelA[1:]
		case 2:
			if len(modelB) == 0 {
				if len(chB) != 0 {
					t.Fatalf("op %d: B has %d, model empty", i, len(chB))
				}
				continue
			}
			if e := (<-chB).(tick); e.N != modelB[0] {
				t.Fatalf("op %d: B got %d, model %d", i, e.N, modelB[0])
			}
			modelB = modelB[1:]
		case 3:
			e, ok := Last[tick](b)
			if ok != haveLast || ok && e.N != lastN {
				t.Fatalf("op %d: Last %v %v, model %v %v", i, e.N, ok, lastN, haveLast)
			}
		}
		if len(chA) != len(modelA) || len(chB) != len(modelB) {
			t.Fatalf("op %d: lens %d/%d, model %d/%d", i, len(chA), len(chB), len(modelA), len(modelB))
		}
		if b.Dropped() != dropped {
			t.Fatalf("op %d: Dropped %d, model %d", i, b.Dropped(), dropped)
		}
	}
}

func TestDropOldestProperty(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for round := 0; round < 300; round++ {
		ops := make([]byte, r.IntN(400))
		for i := range ops {
			ops[i] = byte(r.UintN(256))
		}
		checkDropOldestModel(t, 1+r.IntN(8), 1+r.IntN(32), ops)
	}
}

func FuzzDropOldest(f *testing.F) {
	f.Add(uint8(1), uint8(3), []byte{0, 0, 0, 1, 2, 3, 0, 1, 1})
	f.Add(uint8(4), uint8(1), []byte{0, 0, 0, 0, 0, 0, 2, 2, 1, 1, 1, 1, 3})
	f.Fuzz(func(t *testing.T, a, b uint8, ops []byte) {
		if len(ops) > 4096 {
			ops = ops[:4096]
		}
		checkDropOldestModel(t, 1+int(a%16), 1+int(b%64), ops)
	})
}
