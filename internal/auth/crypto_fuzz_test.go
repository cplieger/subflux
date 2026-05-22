package auth

import "testing"

func FuzzDecrypt(f *testing.F) {
	key := make([]byte, 32)
	// Seed with valid ciphertext from Encrypt.
	plain := []byte("test secret")
	ct, err := Encrypt(plain, key)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(ct)
	f.Add([]byte{})
	f.Add([]byte("short"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Decrypt must not panic on arbitrary input.
		_, _ = Decrypt(data, key)
	})
}
