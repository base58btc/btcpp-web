package main

import (
	"time"

	"btcpp-web/internal/types"
)

func nullableUID(uid uint64) interface{} {
	if uid == 0 {
		return nil
	}
	return int64(uid)
}

func nullableDate(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullableTimesStart(times *types.Times) interface{} {
	if times == nil || times.Start.IsZero() {
		return nil
	}
	return times.Start
}

func nullableTimesEnd(times *types.Times) interface{} {
	if times == nil || times.End == nil || times.End.IsZero() {
		return nil
	}
	return *times.End
}

func dateString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
