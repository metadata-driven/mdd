package tests

import (
	"github.com/goldic/try"
	"github.com/metadata-driven/mdd"
)

type App struct {
	mdd.Application[Context]
}

func NewApp() *App {
	return try.Val(mdd.NewApp[App](Meta))
}
