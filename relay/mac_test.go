package relay

import (
	"testing"
)

func TestComputeMACDeterministic(t *testing.T) {
	key := deriveKey("passphrase", "room")
	msg := ablyMsg{Type: 0, Data: "abc123", Hash: "deadbeef", Sender: "42"}

	mac1 := computeMAC(key, msg)
	mac2 := computeMAC(key, msg)

	if mac1 != mac2 {
		t.Error("computeMAC is not deterministic for the same inputs")
	}
}

func TestVerifyMACValid(t *testing.T) {
	key := deriveKey("passphrase", "room")
	msg := ablyMsg{Type: 0, Data: "abc123", Hash: "deadbeef", Sender: "42"}
	msg.MAC = computeMAC(key, msg)

	if !verifyMAC(key, msg) {
		t.Error("expected valid MAC to verify, but it failed")
	}
}

func TestVerifyMACWrongKey(t *testing.T) {
	key := deriveKey("passphrase-a", "room")
	wrongKey := deriveKey("passphrase-b", "room")

	msg := ablyMsg{Type: 0, Data: "abc123", Hash: "deadbeef", Sender: "42"}
	msg.MAC = computeMAC(key, msg)

	if verifyMAC(wrongKey, msg) {
		t.Error("expected MAC to fail with wrong key, but it verified")
	}
}

func TestVerifyMACTamperedData(t *testing.T) {
	key := deriveKey("passphrase", "room")
	msg := ablyMsg{Type: 0, Data: "abc123", Hash: "deadbeef", Sender: "42"}
	msg.MAC = computeMAC(key, msg)

	msg.Data = "tampered"

	if verifyMAC(key, msg) {
		t.Error("expected MAC to fail after data was tampered, but it verified")
	}
}

func TestVerifyMACTamperedType(t *testing.T) {
	key := deriveKey("passphrase", "room")
	msg := ablyMsg{Type: 0, Data: "abc123", Hash: "deadbeef", Sender: "42"}
	msg.MAC = computeMAC(key, msg)

	msg.Type = 1 // flip text→image

	if verifyMAC(key, msg) {
		t.Error("expected MAC to fail after type was tampered, but it verified")
	}
}

func TestVerifyMACTamperedHash(t *testing.T) {
	key := deriveKey("passphrase", "room")
	msg := ablyMsg{Type: 0, Data: "abc123", Hash: "deadbeef", Sender: "42"}
	msg.MAC = computeMAC(key, msg)

	msg.Hash = "000000"

	if verifyMAC(key, msg) {
		t.Error("expected MAC to fail after hash was tampered, but it verified")
	}
}

func TestVerifyMACTamperedSender(t *testing.T) {
	key := deriveKey("passphrase", "room")
	msg := ablyMsg{Type: 0, Data: "abc123", Hash: "deadbeef", Sender: "42"}
	msg.MAC = computeMAC(key, msg)

	msg.Sender = "99999"

	if verifyMAC(key, msg) {
		t.Error("expected MAC to fail after sender was tampered, but it verified")
	}
}

func TestVerifyMACEmptyMAC(t *testing.T) {
	key := deriveKey("passphrase", "room")
	msg := ablyMsg{Type: 0, Data: "abc123", Hash: "deadbeef", Sender: "42", MAC: ""}

	if verifyMAC(key, msg) {
		t.Error("expected empty MAC to fail verification, but it verified")
	}
}

func TestMACDifferentRoomKeys(t *testing.T) {
	// Same message, same passphrase — different room keys must produce different MACs.
	keyA := deriveKey("passphrase", "room-a")
	keyB := deriveKey("passphrase", "room-b")
	msg := ablyMsg{Type: 0, Data: "abc123", Hash: "deadbeef", Sender: "42"}

	macA := computeMAC(keyA, msg)
	macB := computeMAC(keyB, msg)

	if macA == macB {
		t.Error("expected different room keys to produce different MACs")
	}
}
