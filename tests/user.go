package tests

import (
	"strings"

	"github.com/metadata-driven/mdd"
)

type User struct {
	mdd.Object
}

var _ = mdd.RegisterModel[User](Meta, "user")

func (ctx *Context) Users() *mdd.Set[User] { return mdd.NewSet[User](ctx) }
func (ctx *Context) UserByID(id uint64) *User {
	return ctx.Users().Get(id)
}

var UserSuperAdmin = mdd.Item[User](Meta, "user.super_admin")

func (u *User) Context() *Context              { return mdd.Ctx[Context](u) }
func (u *User) ID() uint64                     { return mdd.ID(u) }
func (u *User) Created() Time                  { return mdd.Attr[Time](u, "created") }
func (u *User) Active() bool                   { return mdd.Attr[bool](u, "active") }
func (u *User) Login() string                  { return mdd.Attr[string](u, "login") }
func (u *User) Name() string                   { return mdd.Attr[string](u, "name") }
func (u *User) Birthday() Time                 { return mdd.Attr[Time](u, "birthday") }
func (u *User) Score() int64                   { return mdd.Attr[int64](u, "score") }
func (u *User) Avatar() mdd.File               { return mdd.Attr[mdd.File](u, "avatar") }
func (u *User) Token() []byte                  { return mdd.Attr[[]byte](u, "token") }
func (u *User) Online() bool                   { return mdd.Attr[bool](u, "online") }
func (u *User) LastSeen() Time                 { return mdd.Attr[Time](u, "last_seen") }
func (u *User) Latitude() float64              { return mdd.Attr[float64](u, "latitude") }
func (u *User) Longitude() float64             { return mdd.Attr[float64](u, "longitude") }
func (u *User) Manager() *User                 { return mdd.Attr[*User](u, "manager") }
func (u *User) Friends() []*User               { return mdd.Attr[[]*User](u, "friends") }
func (u *User) Tags() []string                 { return mdd.Attr[[]string](u, "tags") }
func (u *User) Type() *UserType                { return mdd.Attr[*UserType](u, "type") }
func (u *User) PreviousType() *UserType        { return mdd.Attr[*UserType](u, "previous_type") }
func (u *User) Permissions() []*UserPermission { return mdd.Attr[[]*UserPermission](u, "permissions") }

func (u *User) SetCreated(v Time) *User                  { return mdd.SetAttr(u, "created", v) }
func (u *User) SetActive(v bool) *User                   { return mdd.SetAttr(u, "active", v) }
func (u *User) SetLogin(v string) *User                  { return mdd.SetAttr(u, "login", v) }
func (u *User) SetName(v string) *User                   { return mdd.SetAttr(u, "name", v) }
func (u *User) SetBirthday(v Time) *User                 { return mdd.SetAttr(u, "birthday", v) }
func (u *User) SetScore(v int64) *User                   { return mdd.SetAttr(u, "score", v) }
func (u *User) SetAvatar(v any) *User                    { return mdd.SetAttr(u, "avatar", v) }
func (u *User) SetToken(v []byte) *User                  { return mdd.SetAttr(u, "token", v) }
func (u *User) SetOnline(v bool) *User                   { return mdd.SetAttr(u, "online", v) }
func (u *User) SetLastSeen(v Time) *User                 { return mdd.SetAttr(u, "last_seen", v) }
func (u *User) SetLatitude(v float64) *User              { return mdd.SetAttr(u, "latitude", v) }
func (u *User) SetLongitude(v float64) *User             { return mdd.SetAttr(u, "longitude", v) }
func (u *User) SetManager(v *User) *User                 { return mdd.SetAttr(u, "manager", v) }
func (u *User) SetFriends(v []*User) *User               { return mdd.SetAttr(u, "friends", v) }
func (u *User) SetTags(v []string) *User                 { return mdd.SetAttr(u, "tags", v) }
func (u *User) SetType(v *UserType) *User                { return mdd.SetAttr(u, "type", v) }
func (u *User) SetPreviousType(v *UserType) *User        { return mdd.SetAttr(u, "previous_type", v) }
func (u *User) SetPermissions(v []*UserPermission) *User { return mdd.SetAttr(u, "permissions", v) }

func (u *User) Orders() *mdd.Set[Order]          { return mdd.AttrSet[Order](u, "orders") }
func (u *User) RecentOrders() *mdd.Set[Order]    { return mdd.AttrSet[Order](u, "recent_orders") }
func (u *User) PreferredOrders() *mdd.Set[Order] { return mdd.AttrSet[Order](u, "preferred_orders") }

func (u *User) IsAdmin() bool { return u.Type() == UserTypeAdmin }
func (u *User) IsUser() bool  { return u.Type() == UserTypeUser }
func (u *User) IsGuest() bool { return u.Type() == UserTypeGuest }

func (u *User) OnCommit() {
	if u == nil {
		return
	}
	if u.Name() == "" {
		login := u.Login()
		u.SetName(strings.ToUpper(login[:1]) + login[1:])
	}
}

func (u *User) PreferredStatus() string {
	if u == nil {
		return ""
	}
	if u.Login() == "alice" {
		return "placed"
	}
	return "draft"
}
