package crypto

import (
	"github.com/Luzifer/go-openssl"
)

/*CBC加密 按照golang标准库的例子代码
不过里面没有填充的部分,所以补上
*/
var opensslOpen = openssl.New()


//AesDecrypt aes加密，填充秘钥key的16位，24,32分别对应AES-128, AES-192, or AES-256.
//前端对应 crypto-js
func AesDecrypt(rawData,key []byte) ([]byte, error) {
	dec, err := opensslOpen.DecryptBytes(string(key), rawData)
	if err != nil {
		return nil, err
	}
	return dec, nil
}
