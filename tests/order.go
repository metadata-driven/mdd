package tests

import (
	"github.com/metadata-driven/mdd"
)

type Order struct {
	mdd.Object
}

var _ = mdd.RegisterModel[Order](Meta, "order")

func (ctx *Context) Orders() *mdd.Set[Order] {
	return mdd.NewSet[Order](ctx)
}

func (o *Order) Context() *Context        { return mdd.Ctx[Context](o) }
func (o *Order) ID() uint64               { return mdd.ID(o) }
func (o *Order) Ts() Time                 { return mdd.Attr[Time](o, "ts") }
func (o *Order) User() *User              { return mdd.Attr[*User](o, "user") }
func (o *Order) Status() *OrderStatus     { return mdd.Attr[*OrderStatus](o, "status") }
func (o *Order) Currency() *OrderCurrency { return mdd.Attr[*OrderCurrency](o, "currency") }
func (o *Order) Amount() float64          { return mdd.Attr[float64](o, "amount") }
func (o *Order) Paid() bool               { return mdd.Attr[bool](o, "paid") }
func (o *Order) ValidFor() *UserType      { return mdd.Attr[*UserType](o, "valid_for") }

func (o *Order) SetTs(v Time) *Order                 { return mdd.SetAttr(o, "ts", v) }
func (o *Order) SetUser(v *User) *Order              { return mdd.SetAttr(o, "user", v) }
func (o *Order) SetStatus(v any) *Order              { return mdd.SetAttr(o, "status", v) }
func (o *Order) SetCurrency(v *OrderCurrency) *Order { return mdd.SetAttr(o, "currency", v) }
func (o *Order) SetAmount(v float64) *Order          { return mdd.SetAttr(o, "amount", v) }
func (o *Order) SetPaid(v bool) *Order               { return mdd.SetAttr(o, "paid", v) }
func (o *Order) SetValidFor(v *UserType) *Order      { return mdd.SetAttr(o, "valid_for", v) }
