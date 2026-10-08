package crypto

import (
	"fmt"
	"github.com/Luzifer/go-openssl"
	"testing"
)

func TestAesDecrypt(t *testing.T) {
	encrypted := "U2FsdGVkX19P5N8rr20PXa1Qw0mxX8lzjlLhLskG8sg="
	secret := "1234567812345678"

	o := openssl.New()

	dec, err := o.DecryptBytes(secret, []byte(encrypted))
	if err != nil {
		fmt.Printf("An error occurred: %s\n", err)
	}

	fmt.Printf("Decrypted text: %s\n", string(dec))


}

func TestAesDecryptNot64(t *testing.T) {
	//encryptData := []byte("diM7FcTe4uzMuGemuqfOsFFJVm57oB7YzhDDgTI6b36Q/ppl9yPlv101eeQdtvHZ")
	//keyData := []byte("KX21nJ6M4KMQDBIK")

	//decryptData, _ := AesDecrypt(encryptData, keyData)
	//fmt.Println("decryptData: ", decryptData)

}
