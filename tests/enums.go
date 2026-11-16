package tests

import "github.com/metadata-driven/mdd"

type UserType struct{ mdd.EnumItem }
type UserPermission struct{ mdd.EnumItem }
type OrderStatus struct{ mdd.EnumItem }
type OrderCurrency struct{ mdd.EnumItem }

var (
	UserTypeAdmin = mdd.Item[UserType](Meta, "user.type.admin")
	UserTypeUser  = mdd.Item[UserType](Meta, "user.type.user")
	UserTypeGuest = mdd.Item[UserType](Meta, "user.type.guest")

	UserPermissionManageUsers      = mdd.Item[UserPermission](Meta, "user.permissions.manage_users")
	UserPermissionManageOrders     = mdd.Item[UserPermission](Meta, "user.permissions.manage_orders")
	UserPermissionImpersonateUsers = mdd.Item[UserPermission](Meta, "user.permissions.impersonate_users")

	OrderStatusDraft     = mdd.Item[OrderStatus](Meta, "order.status.draft")
	OrderStatusPlaced    = mdd.Item[OrderStatus](Meta, "order.status.placed")
	OrderStatusCancelled = mdd.Item[OrderStatus](Meta, "order.status.cancelled")

	OrderCurrencyUSD = mdd.Item[OrderCurrency](Meta, "order.currency.usd")
	OrderCurrencyEUR = mdd.Item[OrderCurrency](Meta, "order.currency.eur")
	OrderCurrencyGBP = mdd.Item[OrderCurrency](Meta, "order.currency.gbp")
)
