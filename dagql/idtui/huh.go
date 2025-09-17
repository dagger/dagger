package idtui

import "github.com/charmbracelet/huh"

func NewForm(groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).WithTheme(huh.ThemeBase16())
}
