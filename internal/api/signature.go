package api

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// SecretKey CNTV API签名密钥
	SecretKey = "47899B86370B879139C08EA3B5E88267"
	// FixedUID CNTV API固定UID
	FixedUID = "826D8646DEBBFD97A82D23CAE45A55BE"
	// Version CNTV API版本号
	Version = "2049"
	// EMASAppKey EMAS AppKey
	EMASAppKey = "20000009"
	// EMASecret EMAS HMAC密钥
	EMASecret = "emasgatewayh5"
)

// GenerateCNTVSignature CNTV API MD5签名
// 注意：签名计算必须与请求参数中的uid保持一致
// 使用 "undefined" 而非 FixedUID，以获取可访问的CDN域名
func GenerateCNTVSignature(tsp string) string {
	data := tsp + Version + SecretKey + "undefined"
	hash := md5.Sum([]byte(data))
	return hex.EncodeToString(hash[:])
}

// GenerateEMASSignature CCTVNews EMAS HMAC-SHA256签名
func GenerateEMASSignature(appKey, md5Hash, timestamp, apiName, apiVer string, secret []byte) string {
	signStr := strings.Join([]string{"&&", appKey, md5Hash, timestamp, apiName, apiVer, "&&&&"}, "&")
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signStr))
	return hex.EncodeToString(mac.Sum(nil))
}

// GenerateEMASMD5Hash 生成EMAS签名所需的MD5哈希
func GenerateEMASMD5Hash(params map[string]string) string {
	// 按key排序拼接query string
	queryString := buildSortedQueryString(params)
	hash := md5.Sum([]byte(queryString))
	return hex.EncodeToString(hash[:])
}

// buildSortedQueryString 构建排序后的查询字符串
func buildSortedQueryString(params map[string]string) string {
	// 简化实现：直接拼接
	// 实际应按key排序
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	// 排序
	for i := 0; i < len(keys)-1; i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	// 拼接
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	return strings.Join(parts, "&")
}
