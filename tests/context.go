package tests

import (
	"github.com/metadata-driven/mdd"
)

type Context struct {
	mdd.Context
}

func (ctx *Context) App() *App { return mdd.App[App](ctx) }
