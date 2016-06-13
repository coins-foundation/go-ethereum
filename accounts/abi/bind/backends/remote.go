// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package backends

import (
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
)

// This nil assignment ensures compile time that rpcBackend implements bind.ContractBackend.
var _ bind.ContractBackend = (*rpcBackend)(nil)

// rpcBackend implements bind.ContractBackend, and acts as the data provider to
// Ethereum contracts bound to Go structs. It uses an RPC connection to delegate
// all its functionality.
//
// Note: The current implementation is a blocking one. This should be replaced
// by a proper async version when a real RPC client is created.
type rpcBackend struct {
	client *rpc.Client // RPC client connection to interact with an API server
}

// NewRPCBackend creates a new binding backend to an RPC provider that can be
// used to interact with remote contracts.
func NewRPCBackend(client *rpc.Client) bind.ContractBackend {
	return &rpcBackend{client: client}
}

// ContractCall implements ContractCaller.ContractCall, delegating the execution of
// a contract call to the remote node, returning the reply to for local processing.
func (b *rpcBackend) ContractCall(contract common.Address, data []byte, pending bool) ([]byte, error) {
	// Pack up the request into an RPC argument
	args := struct {
		To   common.Address `json:"to"`
		Data string         `json:"data"`
	}{
		To:   contract,
		Data: common.ToHex(data),
	}
	// Execute the RPC call and retrieve the response
	block := "latest"
	if pending {
		block = "pending"
	}
	var hex string
	err := b.client.Call(&hex, "eth_call", args, block)
	if err != nil {
		return nil, err
	}
	return common.FromHex(hex), nil
}

// PendingAccountNonce implements ContractTransactor.PendingAccountNonce, delegating
// the current account nonce retrieval to the remote node.
func (b *rpcBackend) PendingAccountNonce(account common.Address) (uint64, error) {
	var hex rpc.HexNumber
	err := b.client.Call(&hex, "eth_getTransactionCount", account.Hex(), "pending")
	if err != nil {
		return 0, err
	}
	return hex.Uint64(), nil
}

// SuggestGasPrice implements ContractTransactor.SuggestGasPrice, delegating the
// gas price oracle request to the remote node.
func (b *rpcBackend) SuggestGasPrice() (*big.Int, error) {
	var hex rpc.HexNumber
	if err := b.client.Call(&hex, "eth_gasPrice"); err != nil {
		return nil, err
	}
	return (*big.Int)(&hex), nil
}

// EstimateGasLimit implements ContractTransactor.EstimateGasLimit, delegating
// the gas estimation to the remote node.
func (b *rpcBackend) EstimateGasLimit(sender common.Address, contract *common.Address, value *big.Int, data []byte) (*big.Int, error) {
	// Pack up the request into an RPC argument
	args := struct {
		From  common.Address  `json:"from"`
		To    *common.Address `json:"to"`
		Value *rpc.HexNumber  `json:"value"`
		Data  string          `json:"data"`
	}{
		From:  sender,
		To:    contract,
		Data:  common.ToHex(data),
		Value: rpc.NewHexNumber(value),
	}
	// Execute the RPC call and retrieve the response
	var hex rpc.HexNumber
	err := b.client.Call(&hex, "eth_estimateGas", args)
	if err != nil {
		return nil, err
	}
	return (*big.Int)(&hex), nil
}

// SendTransaction implements ContractTransactor.SendTransaction, delegating the
// raw transaction injection to the remote node.
func (b *rpcBackend) SendTransaction(tx *types.Transaction) error {
	data, err := rlp.EncodeToBytes(tx)
	if err != nil {
		return err
	}
	return b.client.Call(nil, "eth_sendRawTransaction", common.ToHex(data))
}
