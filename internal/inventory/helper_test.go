package inventory

import "time"

func timeout() <-chan time.Time { return time.After(3 * time.Second) }
