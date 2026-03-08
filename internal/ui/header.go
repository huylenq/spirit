package ui

import ()

func RenderHeader(width int, usageBar *UsageBarModel) string {
	if usageBar != nil && usageBar.HasData() {
		bar := usageBar.InlineView(width - 2)
		if bar != "" {
			return HeaderStyle.Width(width).PaddingLeft(1).Render(bar)
		}
	}
	return HeaderStyle.Width(width).Render("")
}
