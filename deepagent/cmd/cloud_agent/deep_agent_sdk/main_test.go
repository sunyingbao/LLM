package apiapp

import "testing"

func TestNewServerSensesClientDisconnection(t *testing.T) {
	if got := NewServer("").GetOptions().SenseClientDisconnection; !got {
		t.Fatalf("SenseClientDisconnection = %v, want true", got)
	}
}

func TestNewServerAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{name: "default", want: defaultServerAddress},
		{name: "configured", address: "127.0.0.1:8080", want: "127.0.0.1:8080"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NewServer(test.address).GetOptions().Addr; got != test.want {
				t.Fatalf("Addr = %q, want %q", got, test.want)
			}
		})
	}
}
