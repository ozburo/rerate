package rerate

import (
	"bytes"
	"strconv"
	"time"

	"github.com/garyburd/redigo/redis"
)

// Counter count total occurs during a period,
// it will store occurs during every time slice interval: (now ~ now - intervl), (now - intervl ~ now - 2*intervl)...
type Counter struct {
	pool     Pool
	pfx      string
	period   time.Duration
	interval time.Duration
	bkts     int
}

// NewCounter create a new Counter
func NewCounter(pool Pool, prefix string, period, interval time.Duration) *Counter {
	return &Counter{
		pool:     pool,
		pfx:      prefix,
		period:   period,
		interval: interval,
		bkts:     int(period/interval) + 1,
	}
}

// hash a time to n buckets(n=c.bkts)
func (c *Counter) hash(t int64) int {
	return int(t/int64(c.interval)) % c.bkts
}

func (c *Counter) key(id string) string {
	buf := bytes.NewBufferString(c.pfx)
	buf.WriteString(":")
	buf.WriteString(id)
	return buf.String()
}

// increment count in specific bucket
func (c *Counter) inc(id string, bucket int) error {
	conn := c.pool.Get()
	defer conn.Close()

	conn.Send("MULTI")
	conn.Send("HINCRBY", c.key(id), strconv.Itoa(bucket), 1)
	conn.Send("HDEL", c.key(id), strconv.Itoa(bucket+1))
	conn.Send("PEXPIRE", c.key(id), int64(c.period/time.Millisecond))
	_, err := conn.Do("EXEC")

	return err
}

// Inc increment id's occurs with current timestamp,
// the count before period will be cleanup
func (c *Counter) Inc(id string) error {
	now := time.Now().UnixNano()
	bucket := c.hash(now)
	return c.inc(id, bucket)
}

// return available buckets
func (c *Counter) buckets(nowbk int) []int {
	rs := make([]int, c.bkts-1)
	for i := 0; i < c.bkts-1; i++ {
		rs[i] = (c.bkts + nowbk - i) % c.bkts
	}
	return rs
}

// Histogram return count histogram in recent period
func (c *Counter) Histogram(id string) ([]int64, error) {
	now := time.Now().UnixNano()
	buckets := c.buckets(c.hash(now))

	args := make([]interface{}, len(buckets)+1)
	args[0] = c.key(id)
	for i, v := range buckets {
		args[i+1] = strconv.Itoa(v)
	}

	conn := c.pool.Get()
	defer conn.Close()

	vals, err := redis.Strings(conn.Do("HMGET", args...))
	if err != nil {
		return []int64{}, err
	}

	ret := make([]int64, len(buckets))
	for i, val := range vals {
		if v, e := strconv.ParseInt(val, 10, 64); e == nil {
			ret[i] = v
		} else {
			ret[i] = 0
		}
	}
	return ret, nil
}

// Count return total occurs in recent period
func (c *Counter) Count(id string) (int64, error) {
	h, err := c.Histogram(id)
	if err != nil {
		return 0, err
	}

	total := int64(0)
	for _, v := range h {
		total += v
	}
	return total, nil
}

// Reset cleanup occurs, set it to zero
func (c *Counter) Reset(id string) error {
	conn := c.pool.Get()
	defer conn.Close()

	_, err := conn.Do("DEL", c.key(id))
	return err
}
