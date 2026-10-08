package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"github.com/pkg/errors"
	"os"
	"strconv"
)


//私钥
var privateKey = []byte(`
-----BEGIN RSA Private Key-----
MIIEpAIBAAKCAQEA1XmxU5BJA4ySGNVUC4uk8E7tNQB+h7/y+twjTdXo+QrdM5vY
csGE8C6TwAEN3WiDNYXWOAY2684DFJjCnUAVlgxpuc8OJyjD2lkOqcm2tFdtbxlN
0g1iB0mRk4MWQBDQCDYlayPlTfAPbP+3hy0nyhrK2YJYsHH2BfoiuWzZYX4Lh60Y
1FhnLvW8+CmQnWJq1t9f8xxoswlcUPblI2pDlpU1eLwlX2m/wClbxvbt10Sbslv5
x8huxhnxd4WsLHzVvVt0ZaD4TUbsnVaED/XDudQ+9vumz4WeP9HgZuogayEeUHfF
TSiXMaB1LzW+M+S7MS1BYJ/gVrpR2QBgIxDudwIDAQABAoIBAQC5rWZig44uSxdM
CElY3XZyGoRR5IMpufgy4ETIz7Ua4Ksz12r1rYLekfSrPX98xAnUBPwlsKoWghLF
2HT156ae418WYZUy1E893NZXMf77b0vdJUo92KwaIJBBmPRCdx0q90WmogsxtgsE
yIsuRUVlkdY8SBayKf1Oh5mqZJXTKAJOvuE6GjX0+Zr5R1ne0NcAACR59H7ZPJq8
f0cYISmtQh05KXHRE9mCJSo55fyBt5FKx1s18PeTemHMCL5wtU8JaqQ9otWCbKnQ
hKYEzdADucXx0UYzqPeBeaVICK2sB03H3zOf0VC8emL1LrMqAL9M8Jv8xq0D9Qen
YtawsmchAoGBAPtEPx3e8aKWS/e/mjqUFREGLWK95M2zob056QuJYKz9C3le5Ev/
uRhHpKcLOtmbSqSg5t+cFg1jD+Kevr3zyAj6A+aeY5dSObP55wQuoVUe8K0/lbes
bdTTng833Zwh/Sv7AHX4qI2Kwu19HvohyfpINmjcH7+9zPfAIkuX0E+RAoGBANl/
MeQWB/C98LuXv9/Bn2uIa80+X90UTuz0VGaczqdjwmQV00FC9UveN+Y94xzoYFQH
uPhbTzsAGAz6BqRiEcTefvkJDzTPg+Pf55hgY8PeW6oPNaWbOw3HL0qSfjwFwNFc
NiuZg3Pum7+g3zu1JDE3t/svsbICIy2gHtgxDOmHAoGAReOsvq1FftjZVpcX2hVY
arzSIPX6z3CYm16hQNE8b8GO0Hqhe7YskOFUnhYUj3SPZY1PyoDK7XxRbdKD8af+
Sujn7ty9jNiVLkdjh5lEzL1nankWNtmiTyFxhIAghw45MmOFtEqu73faUl6MID0H
xjMR10brGdU8TulFYMtgaNECgYBuKfHwUIRnGR4VNrDWOjFweyH3TI+r4Dx14u/Z
JbW6rVnp7fAaDztF2WHA+jnOC5m3Fk5HZaCFBvAnqoCwxIexiu0PYNpV6oIoauHY
mYIO1NLjGV8X6b+IpAo9IGRWLKfUo3tArDob/5DeCDLqAD87urgyv56mxlRhKMhW
wsmCgwKBgQDN74yq+H3CDTUhAASoXIyqdmvP2KINssDM44ST8KpW0bJZnRQvwWyc
MjmgQGsZc8w7B+1p59YgztUP/vJlTVeywKYb5cPyULvxmTafX8qitdFqgZFpNNTI
hHwjPTXOtFeunQ2IW0AGl1hvb4cs6d4AIvVEFuP64T86L+dcTUaHmg==
-----END RSA Private Key-----
`)

//公钥
var publicKey = []byte(`
-----BEGIN RSA Public Key-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA1XmxU5BJA4ySGNVUC4uk
8E7tNQB+h7/y+twjTdXo+QrdM5vYcsGE8C6TwAEN3WiDNYXWOAY2684DFJjCnUAV
lgxpuc8OJyjD2lkOqcm2tFdtbxlN0g1iB0mRk4MWQBDQCDYlayPlTfAPbP+3hy0n
yhrK2YJYsHH2BfoiuWzZYX4Lh60Y1FhnLvW8+CmQnWJq1t9f8xxoswlcUPblI2pD
lpU1eLwlX2m/wClbxvbt10Sbslv5x8huxhnxd4WsLHzVvVt0ZaD4TUbsnVaED/XD
udQ+9vumz4WeP9HgZuogayEeUHfFTSiXMaB1LzW+M+S7MS1BYJ/gVrpR2QBgIxDu
dwIDAQAB
-----END RSA Public Key-----
`)


//GenerateRSAKey 生成RSA私钥和公钥，保存到文件中
// bits 证书大小
func GenerateRSAKey(bits int) {
	//GenerateKey函数使用随机数据生成器random生成一对具有指定字位数的RSA密钥
	//Reader是一个全局、共享的密码用强随机数生成器
	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		panic(err)
	}
	//保存私钥
	//通过x509标准将得到的ras私钥序列化为ASN.1 的 DER编码字符串
	X509PrivateKey := x509.MarshalPKCS1PrivateKey(privateKey)
	//使用pem格式对x509输出的内容进行编码
	//创建文件保存私钥
	privateFile, err := os.Create("private.pem")
	if err != nil {
		panic(err)
	}
	defer privateFile.Close()
	//构建一个pem.Block结构体对象
	privateBlock := pem.Block{Type: "RSA Private Key", Bytes: X509PrivateKey}
	//将数据保存到文件
	pem.Encode(privateFile, &privateBlock)

	//保存公钥
	//获取公钥的数据
	publicKey := privateKey.PublicKey
	//X509对公钥编码
	X509PublicKey, err := x509.MarshalPKIXPublicKey(&publicKey)
	if err != nil {
		panic(err)
	}
	//pem格式编码
	//创建用于保存公钥的文件
	publicFile, err := os.Create("public.pem")
	if err != nil {
		panic(err)
	}
	defer publicFile.Close()
	//创建一个pem.Block结构体对象
	publicBlock := pem.Block{Type: "RSA Public Key", Bytes: X509PublicKey}
	//保存到文件
	pem.Encode(publicFile, &publicBlock)
}

//RsaEncrypt 加密
func RsaEncrypt(origData []byte) ([]byte, error) {
	block, _ := pem.Decode(publicKey) //将密钥解析成公钥实例
	if block == nil {
		return nil, errors.New("public key error")
	}
	pubInterface, err := x509.ParsePKIXPublicKey(block.Bytes) //解析pem.Decode（）返回的Block指针实例
	if err != nil {
		return nil, err
	}
	pub := pubInterface.(*rsa.PublicKey)
	return rsa.EncryptPKCS1v15(rand.Reader, pub, origData) //RSA算法加密
}

//RsaDecrypt 解密
func RsaDecrypt(ciphertext []byte) ([]byte, error) {
	block, _ := pem.Decode(privateKey) //将密钥解析成私钥实例
	if block == nil {
		return nil, errors.New("private key error!")
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes) //解析pem.Decode（）返回的Block指针实例
	if err != nil {
		return nil, err
	}
	return rsa.DecryptPKCS1v15(rand.Reader, priv, ciphertext) //RSA算法解密
}

//RsaDecryptBase 解密
func RsaDecryptBase(ciphertext []byte) ([]byte, error) {
	encryptData, err := base64.StdEncoding.DecodeString(string(ciphertext))
	if err != nil {
		return nil, errors.New("decode base64 string error!")
	}

	block, _ := pem.Decode(privateKey) //将密钥解析成私钥实例
	if block == nil {
		return nil, errors.New("private key error!")
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes) //解析pem.Decode（）返回的Block指针实例
	if err != nil {
		return nil, err
	}
	return rsa.DecryptPKCS1v15(rand.Reader, priv, encryptData) //RSA算法解密
}

//RsaDecryptToString 解密 string
func RsaDecryptToString(ciphertext []byte) (result string, err error) {
	encryptData, err := base64.StdEncoding.DecodeString(string(ciphertext))
	if err != nil {
		err = errors.New("decode base64 string error!")
		return
	}

	block, _ := pem.Decode(privateKey) //将密钥解析成私钥实例
	if block == nil {
		err = errors.New("private key error!")
		return
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes) //解析pem.Decode（）返回的Block指针实例
	if err != nil {
		return
	}
	var d []byte
	if d, err = rsa.DecryptPKCS1v15(rand.Reader, priv, encryptData); err != nil {
		return
	}

	result = string(d)
	return //RSA算法解密
}



//RsaDecryptToFloat64 解密 float64
func RsaDecryptToFloat64(ciphertext []byte) (result float64, err error) {
	encryptData, err := base64.StdEncoding.DecodeString(string(ciphertext))
	if err != nil {
		err = errors.New("decode base64 string error!")
		return
	}

	block, _ := pem.Decode(privateKey) //将密钥解析成私钥实例
	if block == nil {
		err = errors.New("private key error!")
		return
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes) //解析pem.Decode（）返回的Block指针实例
	if err != nil {
		return
	}
	var d []byte
	if d, err = rsa.DecryptPKCS1v15(rand.Reader, priv, encryptData); err != nil {
		return
	}
	if result, err = strconv.ParseFloat(string(d), 64); err != nil {
		return
	}
	return
}