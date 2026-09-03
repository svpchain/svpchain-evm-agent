// Package chain wraps the svpchain gRPC clients used by local registration
// commands. AccountClient supplies account number/sequence and
// BroadcastClient submits signed registration transactions.
//
// gRPC dialing reuses daemons/types.GrpcClientImpl.NewTcpConnection so the
// dial pattern matches existing daemons exactly.
package chain
