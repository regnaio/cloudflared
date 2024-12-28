package v3

import (
	"errors"
	"net"
	"net/netip"
	"sync"

	"github.com/rs/zerolog"
)

var (
	// ErrSessionNotFound indicates that a session has not been registered yet for the request id.
	ErrSessionNotFound = errors.New("flow not found")
	// ErrSessionBoundToOtherConn is returned when a registration already exists for a different connection.
	ErrSessionBoundToOtherConn = errors.New("flow is in use by another connection")
	// ErrSessionAlreadyRegistered is returned when a registration already exists for this connection.
	ErrSessionAlreadyRegistered = errors.New("flow is already registered for this connection")
)

type SessionManager interface {
	// RegisterSession will register a new session if it does not already exist for the request ID.
	// During new session creation, the session will also bind the UDP socket for the origin.
	// If the session exists for a different connection, it will return [ErrSessionBoundToOtherConn].
	RegisterSession(request *UDPSessionRegistrationDatagram, conn DatagramConn) (Session, error)
	// GetSession returns an active session if available for the provided connection.
	// If the session does not exist, it will return [ErrSessionNotFound]. If the session exists for a different
	// connection, it will return [ErrSessionBoundToOtherConn].
	GetSession(requestID RequestID) (Session, error)
	// UnregisterSession will remove a session from the current session manager. It will attempt to close the session
	// before removal.
	UnregisterSession(requestID RequestID)
}

type DialUDP func(dest netip.AddrPort) (*net.UDPConn, error)

type sessionManager struct {
	sessions     map[RequestID]Session
	mutex        sync.RWMutex
	originDialer DialUDP
	metrics      Metrics
	log          *zerolog.Logger
}

func NewSessionManager(metrics Metrics, log *zerolog.Logger, originDialer DialUDP) SessionManager {
	return &sessionManager{
		sessions:     make(map[RequestID]Session),
		originDialer: originDialer,
		metrics:      metrics,
		log:          log,
	}
}

func (s *sessionManager) RegisterSession(request *UDPSessionRegistrationDatagram, conn DatagramConn) (Session, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	// Check to make sure session doesn't already exist for requestID
	if session, exists := s.sessions[request.RequestID]; exists {
		if conn.ID() == session.ConnectionID() {
			return nil, ErrSessionAlreadyRegistered
		}
		return nil, ErrSessionBoundToOtherConn
	}
	// Attempt to bind the UDP socket for the new session
	origin, err := s.originDialer(request.Dest)
	if err != nil {
		return nil, err
	}
	// Create and insert the new session in the map
	session := NewSession(
		request.RequestID,
		request.IdleDurationHint,
		origin,
		origin.RemoteAddr(),
		origin.LocalAddr(),
		conn,
		s.metrics,
		s.log)
	s.sessions[request.RequestID] = session
	return session, nil
}

func (s *sessionManager) GetSession(requestID RequestID) (Session, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	session, exists := s.sessions[requestID]
	if exists {
		return session, nil
	}
	return nil, ErrSessionNotFound
}

func (s *sessionManager) UnregisterSession(requestID RequestID) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	// Get the session and make sure to close it if it isn't already closed
	session, exists := s.sessions[requestID]
	if exists {
		// We ignore any errors when attempting to close the session
		_ = session.Close()
	}
	delete(s.sessions, requestID)
}
