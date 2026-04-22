package api

import "testing"

func TestValidatePasswordCharming123AtHash(t *testing.T) {
	if err := ValidatePassword("Charming123!@#"); err != nil {
		t.Fatal(err)
	}
}
