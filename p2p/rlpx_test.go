// Copyright 2015 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with go-ethereum.  If not, see <http://www.gnu.org/licenses/>.

package p2p

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/rlp"
)

func TestSharedSecret(t *testing.T) {
	prv0, _ := crypto.GenerateKey() // = ecdsa.GenerateKey(crypto.S256(), rand.Reader)
	pub0 := &prv0.PublicKey
	prv1, _ := crypto.GenerateKey()
	pub1 := &prv1.PublicKey

	ss0, err := ecies.ImportECDSA(prv0).GenerateShared(ecies.ImportECDSAPublic(pub1), sskLen, sskLen)
	if err != nil {
		return
	}
	ss1, err := ecies.ImportECDSA(prv1).GenerateShared(ecies.ImportECDSAPublic(pub0), sskLen, sskLen)
	if err != nil {
		return
	}
	t.Logf("Secret:\n%v %x\n%v %x", len(ss0), ss0, len(ss0), ss1)
	if !bytes.Equal(ss0, ss1) {
		t.Errorf("dont match :(")
	}
}

func TestEncHandshake(t *testing.T) {
	for i := 0; i < 10; i++ {
		start := time.Now()
		if err := testEncHandshake(nil); err != nil {
			t.Fatalf("i=%d %v", i, err)
		}
		t.Logf("%d %v\n", i+1, time.Since(start))
	}
}

func testEncHandshake(token []byte) error {
	type result struct {
		side string
		id   discover.NodeID
		err  error
	}
	var (
		prv0, _  = crypto.GenerateKey()
		prv1, _  = crypto.GenerateKey()
		fd0, fd1 = net.Pipe()
		c0, c1   = newRLPX(fd0).(*rlpx), newRLPX(fd1).(*rlpx)
		output   = make(chan result)
	)

	go func() {
		r := result{side: "initiator"}
		defer func() { output <- r }()

		dest := &discover.Node{ID: discover.PubkeyID(&prv1.PublicKey)}
		r.id, r.err = c0.doEncHandshake(prv0, dest)
		if r.err != nil {
			return
		}
		id1 := discover.PubkeyID(&prv1.PublicKey)
		if r.id != id1 {
			r.err = fmt.Errorf("remote ID mismatch: got %v, want: %v", r.id, id1)
		}
	}()
	go func() {
		r := result{side: "receiver"}
		defer func() { output <- r }()

		r.id, r.err = c1.doEncHandshake(prv1, nil)
		if r.err != nil {
			return
		}
		id0 := discover.PubkeyID(&prv0.PublicKey)
		if r.id != id0 {
			r.err = fmt.Errorf("remote ID mismatch: got %v, want: %v", r.id, id0)
		}
	}()

	// wait for results from both sides
	r1, r2 := <-output, <-output
	if r1.err != nil {
		return fmt.Errorf("%s side error: %v", r1.side, r1.err)
	}
	if r2.err != nil {
		return fmt.Errorf("%s side error: %v", r2.side, r2.err)
	}

	// compare derived secrets
	if !reflect.DeepEqual(c0.rw.encIV, c1.rw.decIV) {
		return fmt.Errorf("encIV mismatch:\n c0.encIV: %#v\n c1.decIV: %#v", c0.rw.encIV, c1.rw.decIV)
	}
	if !reflect.DeepEqual(c0.rw.decIV, c1.rw.encIV) {
		return fmt.Errorf("decIV mismatch:\n c0.decIV: %#v\n c1.encIV: %#v", c0.rw.decIV, c1.rw.encIV)
	}
	if !reflect.DeepEqual(c0.rw.enc, c1.rw.dec) {
		return fmt.Errorf("enc cipher mismatch:\n c0.enc: %#v\n c1.dec: %#v", c0.rw.enc, c1.rw.dec)
	}
	if !reflect.DeepEqual(c0.rw.dec, c1.rw.enc) {
		return fmt.Errorf("dec cipher mismatch:\n c0.dec: %#v\n c1.enc: %#v", c0.rw.dec, c1.rw.enc)
	}
	return nil
}

func TestProtocolHandshake(t *testing.T) {
	var (
		prv0, _ = crypto.GenerateKey()
		node0   = &discover.Node{ID: discover.PubkeyID(&prv0.PublicKey), IP: net.IP{1, 2, 3, 4}, TCP: 33}
		hs0     = &protoHandshake{Version: 3, ID: node0.ID, Caps: []Cap{{"a", 0}, {"b", 2}}}

		prv1, _ = crypto.GenerateKey()
		node1   = &discover.Node{ID: discover.PubkeyID(&prv1.PublicKey), IP: net.IP{5, 6, 7, 8}, TCP: 44}
		hs1     = &protoHandshake{Version: 3, ID: node1.ID, Caps: []Cap{{"c", 1}, {"d", 3}}}

		fd0, fd1 = net.Pipe()
		wg       sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		rlpx := newRLPX(fd0)
		remid, err := rlpx.doEncHandshake(prv0, node1)
		if err != nil {
			t.Errorf("dial side enc handshake failed: %v", err)
			return
		}
		if remid != node1.ID {
			t.Errorf("dial side remote id mismatch: got %v, want %v", remid, node1.ID)
			return
		}

		phs, err := rlpx.doProtoHandshake(hs0)
		if err != nil {
			t.Errorf("dial side proto handshake error: %v", err)
			return
		}
		if !reflect.DeepEqual(phs, hs1) {
			t.Errorf("dial side proto handshake mismatch:\ngot: %s\nwant: %s\n", spew.Sdump(phs), spew.Sdump(hs1))
			return
		}
		rlpx.close(DiscQuitting)
	}()
	go func() {
		defer wg.Done()
		rlpx := newRLPX(fd1)
		remid, err := rlpx.doEncHandshake(prv1, nil)
		if err != nil {
			t.Errorf("listen side enc handshake failed: %v", err)
			return
		}
		if remid != node0.ID {
			t.Errorf("listen side remote id mismatch: got %v, want %v", remid, node0.ID)
			return
		}

		phs, err := rlpx.doProtoHandshake(hs1)
		if err != nil {
			t.Errorf("listen side proto handshake error: %v", err)
			return
		}
		if !reflect.DeepEqual(phs, hs0) {
			t.Errorf("listen side proto handshake mismatch:\ngot: %s\nwant: %s\n", spew.Sdump(phs), spew.Sdump(hs0))
			return
		}

		if err := ExpectMsg(rlpx, discMsg, []DiscReason{DiscQuitting}); err != nil {
			t.Errorf("error receiving disconnect: %v", err)
		}
	}()
	wg.Wait()
}

func TestProtocolHandshakeErrors(t *testing.T) {
	our := &protoHandshake{Version: 3, Caps: []Cap{{"foo", 2}, {"bar", 3}}, Name: "quux"}
	id := randomID()
	tests := []struct {
		code uint64
		msg  interface{}
		err  error
	}{
		{
			code: discMsg,
			msg:  []DiscReason{DiscQuitting},
			err:  DiscQuitting,
		},
		{
			code: 0x989898,
			msg:  []byte{1},
			err:  errors.New("expected handshake, got 989898"),
		},
		{
			code: handshakeMsg,
			msg:  make([]byte, baseProtocolMaxMsgSize+2),
			err:  errors.New("message too big"),
		},
		{
			code: handshakeMsg,
			msg:  []byte{1, 2, 3},
			err:  newPeerError(errInvalidMsg, "(code 0) (size 4) rlp: expected input list for p2p.protoHandshake"),
		},
		{
			code: handshakeMsg,
			msg:  &protoHandshake{Version: 9944, ID: id},
			err:  DiscIncompatibleVersion,
		},
		{
			code: handshakeMsg,
			msg:  &protoHandshake{Version: 3},
			err:  DiscInvalidIdentity,
		},
	}

	for i, test := range tests {
		p1, p2 := MsgPipe()
		go Send(p1, test.code, test.msg)
		_, err := readProtocolHandshake(p2, our)
		if !reflect.DeepEqual(err, test.err) {
			t.Errorf("test %d: error mismatch: got %q, want %q", i, err, test.err)
		}
	}
}

func TestRLPXFrameRW(t *testing.T) {
	var s1, s2 secrets
	s1.AES1 = make([]byte, 32)
	s1.AES2 = make([]byte, 32)
	rand.Read(s1.AES1)
	rand.Read(s2.AES2)
	conn := new(bytes.Buffer)
	rw1 := newRLPXFrameRW(conn, s1)

	s2.AES1 = common.CopyBytes(s1.AES2)
	s2.AES2 = common.CopyBytes(s1.AES1)
	rw2 := newRLPXFrameRW(conn, s2)

	// send some messages
	for i := 0; i < 10; i++ {
		// write message into conn buffer
		wmsg := []interface{}{"foo", "bar", strings.Repeat("test", i)}
		err := Send(rw1, uint64(i), wmsg)
		if err != nil {
			t.Fatalf("WriteMsg error (i=%d): %v", i, err)
		}

		// read message that rw1 just wrote
		msg, err := rw2.ReadMsg()
		if err != nil {
			t.Fatalf("ReadMsg error (i=%d): %v", i, err)
		}
		if msg.Code != uint64(i) {
			t.Fatalf("msg code mismatch: got %d, want %d", msg.Code, i)
		}
		payload, _ := ioutil.ReadAll(msg.Payload)
		wantPayload, _ := rlp.EncodeToBytes(wmsg)
		if !bytes.Equal(payload, wantPayload) {
			t.Fatalf("msg payload mismatch:\ngot  %x\nwant %x", payload, wantPayload)
		}
	}
}
