package kubevirt

import "crypto/rand"

// RandomVMName generates a name like "vm-a1b2". The prefix is lowercase
// so the result is always a valid DNS label (see ValidateVMName).
func RandomVMName() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, VMNameRandomLength)
	if _, err := rand.Read(b); err != nil {
		return VMNamePrefix + "0000"
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return VMNamePrefix + string(b)
}
