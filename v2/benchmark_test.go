package carve

import (
	"context"
	"strings"
	"testing"
)

func BenchmarkRenderSize(b *testing.B) {
	for _, size := range []int{2048, 8192, 32768} {
		source := strings.Repeat("Plain Carve text.\n", size/18+1)[:size]
		name := map[int]string{2048: "2k", 8192: "8k", 32768: "32k"}[size]
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := renderHTML(context.Background(), source, Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
