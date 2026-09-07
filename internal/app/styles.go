package app

import "charm.land/lipgloss/v2"

var headerStyle = lipgloss.NewStyle().Bold(true)
var toolLabelStyle = lipgloss.NewStyle().Bold(true)
var toolStatusStyle = lipgloss.NewStyle().Faint(true)

func transcriptStyle(width int) lipgloss.Style {
	if width < 1 {
		width = 1
	}
	return lipgloss.NewStyle().Width(width)
}
