package serialiser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type serialisedValue struct {
	Name string `json:"name"`
}

func TestJSONSerialisers(t *testing.T) {
	require.Equal(t, `{"name":"value"}`, ToStringIgnore(serialisedValue{Name: "value"}))
	require.Empty(t, ToStringIgnore(make(chan int)))
	require.Equal(t, "{}", ToString(nil))
	require.Equal(t, `{"name":"value"}`, ToString(serialisedValue{Name: "value"}))
	require.Equal(t, []byte(`{"name":"value"}`), ToBytes(serialisedValue{Name: "value"}))
	require.Nil(t, ToBytes(make(chan int)))

	var target serialisedValue
	require.NoError(t, FromString(`{"name":"decoded"}`, &target))
	require.Equal(t, serialisedValue{Name: "decoded"}, target)
	require.Error(t, FromString(`{"name":`, &target))

	decoded, err := ToStruct[serialisedValue](`{"name":"typed"}`)
	require.NoError(t, err)
	require.Equal(t, serialisedValue{Name: "typed"}, decoded)
	decoded, err = ToStruct[serialisedValue]("")
	require.NoError(t, err)
	require.Equal(t, serialisedValue{}, decoded)
	_, err = ToStruct[serialisedValue](`{"name":`)
	require.Error(t, err)
}
