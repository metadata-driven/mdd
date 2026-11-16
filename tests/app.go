package tests

import (
	"time"

	"github.com/goldic/try"
	"github.com/metadata-driven/mdd"
)

type App struct {
	mdd.Application[Context]
}

type Time = time.Time

func NewApp() *App {
	return try.Val(mdd.NewApp[App](Meta))
}
