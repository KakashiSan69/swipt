package device

import "time"

func (s *Connection) trace(at time.Time, name, detail string, duration time.Duration) {
	s.logger.Debug("trace.", name, ": ", detail, " (", duration, ")")
}
