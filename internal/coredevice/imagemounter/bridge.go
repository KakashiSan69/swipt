package imagemounter

import (
	"context"
	"errors"
	"io"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/swipt-automation/swipt/internal/coredevice/xpc"
)

// mobile_storage_proxy reads XPCRequestRecvTx from the outer dictionary,
// alongside XPCRequestDictionary. ReceiveBytes starts that file transfer
// without sending ReceiveBytesAck; its completion replies Status=Complete
// (iOS 17.7 21H16, 0x100009998..0x1000099cc, 0x10000a914..0x10000a928,
// 0x10000b31c, 0x10000b590..0x10000b628).
func (m *mounterConn) uploadBridge(ctx context.Context, content io.Reader, size int64, ticket []byte) error {
	const command = "ReceiveBytes"
	const transferID = 1
	err := m.bridge.SendContext(ctx, map[string]any{
		"XPCRequestDictionary": map[string]any{
			"Command":         command,
			"HostProcessName": "swipt",
			"ImageSignature":  ticket,
			"ImageType":       personalizedImageType,
		},
		"XPCRequestRecvTx": xpc.FileTransfer{ID: transferID, Size: uint64(size)},
	}, true)
	if err != nil {
		return E.Cause(err, "imagemounter: send ", command)
	}
	transferCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sent := make(chan error, 1)
	go func() {
		err := m.bridge.SendFile(transferCtx, transferID, &contextReader{ctx: transferCtx, reader: content}, size)
		if err != nil {
			m.bridge.Close()
		}
		sent <- err
	}()
	reply, receiveErr := m.receive(command)
	if receiveErr == nil {
		receiveErr = m.expectStatus(command, reply, "Complete")
	}
	if receiveErr != nil {
		cancel()
	}
	sendErr := <-sent
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, isDeviceError := errors.AsType[*DeviceError](receiveErr); receiveErr != nil && (isDeviceError || E.IsCanceled(sendErr)) {
		return receiveErr
	}
	if sendErr != nil {
		return E.Cause(sendErr, "imagemounter: upload the image")
	}
	return receiveErr
}
