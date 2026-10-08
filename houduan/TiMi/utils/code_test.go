package utils

import (
	"fmt"
	"testing"
)

func TestGetString(t *testing.T) {
	for i := 0; i < 5; i++ {
		fmt.Println(GetString(8))
	}
}