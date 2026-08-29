package kglib

import "testing"

func TestEmbeddingFromRaw(t *testing.T) {
	if emb, ok := embeddingFromRaw([]any{float32(1.5), float32(-2)}); !ok || emb[0] != 1.5 || emb[1] != -2 {
		t.Errorf("float32 components: emb=%v ok=%v, want [1.5 -2] true", emb, ok)
	}
	// float64 components must convert, not zero — a DOUBLE column or driver
	// change silently corrupting distances is the bug this guards.
	if emb, ok := embeddingFromRaw([]any{float64(1.5), float64(-2)}); !ok || emb[0] != 1.5 || emb[1] != -2 {
		t.Errorf("float64 components: emb=%v ok=%v, want [1.5 -2] true", emb, ok)
	}
	if emb, ok := embeddingFromRaw([]any{float32(1), float64(2)}); !ok || emb[1] != 2 {
		t.Errorf("mixed components: emb=%v ok=%v, want [1 2] true", emb, ok)
	}
	// An unhandled type must reject the vector, never zero the component.
	if _, ok := embeddingFromRaw([]any{float32(1), "not a number"}); ok {
		t.Error("string component accepted; must return ok=false")
	}
	if _, ok := embeddingFromRaw([]any{int64(3)}); ok {
		t.Error("int64 component accepted; must return ok=false")
	}
}
