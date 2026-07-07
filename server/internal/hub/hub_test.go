package hub

import "testing"

func TestSceneClaimReleaseAndUnregister(t *testing.T) {
	broker := NewBroker()
	alice := &Client{
		UserID:    1,
		Username:  "alice",
		PeerID:    "session-a",
		ProjectID: 7,
		Send:      make(chan []byte, 1),
	}
	bob := &Client{
		UserID:    2,
		Username:  "bob",
		PeerID:    "session-b",
		ProjectID: 7,
		Send:      make(chan []byte, 1),
	}

	broker.Register(alice)
	broker.Register(bob)
	owner, claimed := broker.ClaimScene(alice, "levels/main.tscn")
	if !claimed || owner.PeerID != alice.PeerID {
		t.Fatalf("alice should claim scene, got claimed=%v owner=%+v", claimed, owner)
	}

	owner, claimed = broker.ClaimScene(bob, "levels/main.tscn")
	if claimed || owner.PeerID != alice.PeerID {
		t.Fatalf("bob should see alice as owner, got claimed=%v owner=%+v", claimed, owner)
	}
	if broker.ReleaseScene(bob, "levels/main.tscn") {
		t.Fatal("non-owner should not release scene")
	}
	if !broker.ReleaseScene(alice, "levels/main.tscn") {
		t.Fatal("owner should release scene")
	}

	owner, claimed = broker.ClaimScene(bob, "levels/main.tscn")
	if !claimed || owner.PeerID != bob.PeerID {
		t.Fatalf("bob should claim after release, got claimed=%v owner=%+v", claimed, owner)
	}
	broker.Unregister(bob)
	if owner, ok := broker.GetSceneOwner(7, "levels/main.tscn"); ok {
		t.Fatalf("unregister should release owned scene, got owner=%+v", owner)
	}
}
