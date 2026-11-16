package management

import (
	"context"
	"testing"

	"github.com/netbirdio/netbird/encryption"
	nbproto "github.com/netbirdio/netbird/shared/management/proto"
	"github.com/stretchr/testify/require"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestEncryptedLoginMatchesOnlyTheDecryptedSetupKeyAndRejectsTampering(t *testing.T) {
	const setupKey = "example-setup-key"
	const peerAddress = "100.64.0.10/16"

	serverKey, err := wgtypes.GeneratePrivateKey()
	require.NoError(t, err)

	peerKey, err := wgtypes.GeneratePrivateKey()
	require.NoError(t, err)

	serverPublicKey := serverKey.PublicKey()

	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory().Silent().RequestParsers(&LoginRequestParser{serverKey: serverKey}))
	m.MustStart()
	t.Cleanup(m.Close)

	accepted := m.MustMock(mockgrpc.Method(_loginMethod).
		Field("setupKey", match.Equal(setupKey)).
		Reply(mockgrpc.Func(func(_ context.Context, call *mockgrpc.Call) error {
			// The original envelope carries the peer public key needed to encrypt the reply.
			_, peerPublicKey, err := readEnvelope(call.Message())
			if err != nil {
				return err
			}
			response := &nbproto.LoginResponse{PeerConfig: &nbproto.PeerConfig{Address: peerAddress}}
			body, err := encryption.EncryptMessage(peerPublicKey, serverKey, response)
			if err != nil {
				return status.Errorf(codes.Internal, "login: encrypting the reply: %v", err)
			}
			return call.Send(&nbproto.EncryptedMessage{WgPubKey: serverPublicKey.String(), Body: body})
		})))

	client := nbproto.NewManagementServiceClient(m.MustDial())

	request := &nbproto.LoginRequest{SetupKey: setupKey}
	first := sealLogin(t, request, serverPublicKey, peerKey)
	second := sealLogin(t, request, serverPublicKey, peerKey)
	require.NotEqual(t, first.GetBody(), second.GetBody())

	for _, envelope := range []*nbproto.EncryptedMessage{first, second} {
		reply, err := client.Login(t.Context(), envelope)
		require.NoError(t, err)
		require.Equal(t, serverPublicKey.String(), reply.GetWgPubKey())

		var response nbproto.LoginResponse
		err = encryption.DecryptMessage(serverPublicKey, peerKey, reply.GetBody(), &response)
		require.NoError(t, err)
		require.Equal(t, peerAddress, response.GetPeerConfig().GetAddress())
	}

	other := sealLogin(t, &nbproto.LoginRequest{SetupKey: "another-setup-key"}, serverPublicKey, peerKey)
	_, err = client.Login(t.Context(), other)
	require.Equal(t, codes.NotFound, status.Code(err))

	tampered := proto.CloneOf(first)
	tampered.Body[len(tampered.Body)-1] ^= 1

	_, err = client.Login(t.Context(), tampered)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "login: invalid request ciphertext", status.Convert(err).Message())

	accepted.AssertNumberOfCalls(t, 2)
}

func sealLogin(t *testing.T, request *nbproto.LoginRequest, serverPublicKey, peerKey wgtypes.Key) *nbproto.EncryptedMessage {
	t.Helper()

	body, err := encryption.EncryptMessage(serverPublicKey, peerKey, request)
	require.NoError(t, err)

	return &nbproto.EncryptedMessage{WgPubKey: peerKey.PublicKey().String(), Body: body}
}
