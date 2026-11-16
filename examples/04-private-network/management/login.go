// Package management mocks NetBird's management service, whose gRPC payloads are encrypted end to end.
package management

import (
	"context"

	"github.com/netbirdio/netbird/encryption"
	nbproto "github.com/netbirdio/netbird/shared/management/proto"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte/mockgrpc"
)

var _ mockgrpc.RequestParser = (*LoginRequestParser)(nil)

// _loginMethod is the full gRPC method name of the management service Login RPC.
const _loginMethod = "/management.ManagementService/Login"

// LoginRequestParser is a [mockgrpc.RequestParser] that decrypts a Login [nbproto.EncryptedMessage] into its
// [nbproto.LoginRequest], so mocks can match plain fields such as setupKey.
type LoginRequestParser struct {
	// serverKey is the WireGuard private key the mock server uses to decrypt requests.
	serverKey wgtypes.Key
}

// CanParse reports whether call is a Login call.
func (p *LoginRequestParser) CanParse(call *mockgrpc.Call, _ proto.Message) bool {
	return call.Method() == _loginMethod
}

// Parse decrypts msg with the sender's public key and the server key and returns the [nbproto.LoginRequest].
// It fails with [codes.InvalidArgument] when msg is not an encrypted message, carries an invalid key, or does not
// decrypt.
func (p *LoginRequestParser) Parse(_ context.Context, _ *mockgrpc.Call, msg proto.Message) (any, error) {
	envelope, peerPublicKey, err := readEnvelope(msg)
	if err != nil {
		return nil, err
	}

	request := &nbproto.LoginRequest{}
	if err := encryption.DecryptMessage(peerPublicKey, p.serverKey, envelope.GetBody(), request); err != nil {
		return nil, status.Error(codes.InvalidArgument, "login: invalid request ciphertext")
	}

	return request, nil
}

// readEnvelope returns msg as an [nbproto.EncryptedMessage] with the sender's public key, which a reply also needs to
// encrypt its body for that peer. It fails with [codes.InvalidArgument] on any other message or an invalid key.
func readEnvelope(msg proto.Message) (*nbproto.EncryptedMessage, wgtypes.Key, error) {
	envelope, ok := msg.(*nbproto.EncryptedMessage)
	if !ok {
		return nil, wgtypes.Key{}, status.Error(codes.InvalidArgument, "login: expected an encrypted message")
	}

	peerPublicKey, err := wgtypes.ParseKey(envelope.GetWgPubKey())
	if err != nil {
		return nil, wgtypes.Key{}, status.Error(codes.InvalidArgument, "login: invalid peer public key")
	}

	return envelope, peerPublicKey, nil
}
