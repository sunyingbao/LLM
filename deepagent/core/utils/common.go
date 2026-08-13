package utils

import (
	"code.byted.org/gopkg/logs/v2/log"
	"github.com/bytedance/sonic"
)

// JSON 序列化/反序列化相关函数
// ----------------------------------------

// ToStringIgnore 将对象序列化为JSON字符串，忽略错误
func ToStringIgnore(val interface{}) string {
	str, err := sonic.MarshalString(val)
	if err != nil {
		return ""
	}
	return str
}

// ToString 将对象序列化为JSON字符串
func ToString(val interface{}) string {
	if val == nil {
		return "{}"
	}
	str, _ := sonic.MarshalString(val)
	return str
}

func ToBytes(val interface{}) []byte {
	b, _ := sonic.Marshal(val)
	return b
}

// FromString 将JSON字符串反序列化为对象
func FromString(str string, i interface{}) error {
	err := sonic.Unmarshal([]byte(str), i)
	if err != nil {
		log.V2.Error().KVs("error_msg", "sonic.Unmarshal error", "arg", str).Error(err).Emit()
		return err
	}
	return nil
}

// ToStruct 将 JSON 字符串反序列化为指定类型（泛型版本）
// ToStruct deserializes JSON string to specified type (generic version)
func ToStruct[T any](str string) (T, error) {
	var result T
	if str == "" {
		return result, nil
	}
	err := sonic.UnmarshalString(str, &result)
	if err != nil {
		return result, err
	}
	return result, nil
}
