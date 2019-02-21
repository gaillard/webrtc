package webrtc

import (
	"fmt"
	"sync"

	"github.com/pions/rtcp"
)

// RTPSender allows an application to control how a given Track is encoded and transmitted to a remote peer
type RTPSender struct {
	track          *Track
	rtcpReadStream *lossyReadCloser

	transport *DTLSTransport

	// A reference to the associated api object
	api *API

	mu                     sync.RWMutex
	sendCalled, stopCalled bool
}

// NewRTPSender constructs a new RTPSender
func (api *API) NewRTPSender(track *Track, transport *DTLSTransport) (*RTPSender, error) {
	if track == nil {
		return nil, fmt.Errorf("Track must not be nil")
	} else if transport == nil {
		return nil, fmt.Errorf("DTLSTransport must not be nil")
	}

	track.mu.RLock()
	defer track.mu.RUnlock()
	if track.receiver != nil {
		return nil, fmt.Errorf("RTPSender can not be constructed with remote track")
	}

	return &RTPSender{
		track:     track,
		transport: transport,
		api:       api,
	}, nil
}

// Send Attempts to set the parameters controlling the sending of media.
func (r *RTPSender) Send(parameters RTPSendParameters) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.sendCalled {
		return fmt.Errorf("Send has already been called")
	}
	r.sendCalled = true

	srtcpSession, err := r.transport.getSRTCPSession()
	if err != nil {
		return err
	}
	srtcpReadStream, err := srtcpSession.OpenReadStream(parameters.encodings.SSRC)
	if err != nil {
		return err
	}
	r.rtcpReadStream = newLossyReadCloser(srtcpReadStream)

	r.track.mu.Lock()
	r.track.senders = append(r.track.senders, r)
	r.track.mu.Unlock()

	return nil
}

// Stop irreversibly stops the RTPSender
func (r *RTPSender) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stopCalled {
		return fmt.Errorf("Stop has already been called")
	} else if !r.sendCalled {
		return fmt.Errorf("RTPSender was never started")
	}
	r.stopCalled = true

	r.track.mu.Lock()
	defer r.track.mu.Unlock()
	filtered := []*RTPSender{}
	for _, s := range r.track.senders {
		if s != r {
			filtered = append(filtered, s)
		}
	}
	r.track.senders = filtered
	return r.rtcpReadStream.close()
}

// Read reads incoming RTCP for this RTPReceiver
func (r *RTPSender) Read(b []byte) (n int, err error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	switch {
	case !r.sendCalled:
		return 0, fmt.Errorf("RTPSender never got a send call")
	case r.stopCalled:
		return 0, fmt.Errorf("RTPReceiver is stopped")
	default:
		return r.rtcpReadStream.read(b)
	}
}

// ReadRTCP is a convenience method that wraps Read and unmarshals for you
func (r *RTPSender) ReadRTCP(b []byte) (rtcp.Packet, rtcp.Header, error) {
	i, err := r.Read(b)
	if err != nil {
		return nil, rtcp.Header{}, err
	}

	return rtcp.Unmarshal(b[:i])
}

// sendRTP should only be called by a track, this only exists so we can keep state in one place
func (r *RTPSender) sendRTP(b []byte) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if !r.sendCalled {
		return 0, fmt.Errorf("RTPSender was never started")
	} else if r.stopCalled {
		return 0, fmt.Errorf("RTPSender has been stopped")
	}

	srtpSession, err := r.transport.getSRTPSession()
	if err != nil {
		return 0, err
	}

	writeStream, err := srtpSession.OpenWriteStream()
	if err != nil {
		return 0, err
	}

	return writeStream.Write(b)
}
