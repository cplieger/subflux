package auth

import "testing"

func BenchmarkHashPassword(b *testing.B) {
	for range b.N {
		_, err := HashPassword("benchmark-password-123!")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerifyPassword(b *testing.B) {
	hash, err := HashPassword("benchmark-password-123!")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		_, _ = VerifyPassword("benchmark-password-123!", hash)
	}
}
