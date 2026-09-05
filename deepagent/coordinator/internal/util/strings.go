package util

import "github.com/bytedance/sonic"

// ToString 将对象序列化为JSON字符串
func ToString(val interface{}) string {
	str, _ := sonic.MarshalString(val)
	return str
}

// ToStruct 将 JSON 字符串反序列化为指定类型（泛型版本）
// ToStruct deserializes JSON string to specified type (generic version)
func ToStruct[T any](str string) (*T, error) {
	var result T
	err := sonic.UnmarshalString(str, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
