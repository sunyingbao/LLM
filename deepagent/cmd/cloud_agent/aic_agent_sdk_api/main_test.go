package main

import "testing"

func TestNewServerSensesClientDisconnection(t *testing.T) {
	if got := newServer().GetOptions().SenseClientDisconnection; !got {
		t.Fatalf("SenseClientDisconnection = %v, want true", got)
	}
}
