package handlers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func utilityCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:                     "say",
			Description:              "Send a message as the bot in a specific channel",
			DefaultMemberPermissions: &adminPerm,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Channel to send the message in", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "message", Description: "Message content", Required: true},
			},
		},
		{
			Name:                     "embed",
			Description:              "Create and send a custom embed in a specific channel",
			DefaultMemberPermissions: &adminPerm,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Channel to send the embed in", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "title", Description: "Embed title", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "description", Description: "Embed description (use \\n for new lines)", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "colour", Description: "Hex colour (e.g. #ff0000)"},
				{Type: discordgo.ApplicationCommandOptionString, Name: "image", Description: "Image URL"},
				{Type: discordgo.ApplicationCommandOptionString, Name: "thumbnail", Description: "Thumbnail URL"},
				{Type: discordgo.ApplicationCommandOptionString, Name: "footer", Description: "Footer text"},
				{Type: discordgo.ApplicationCommandOptionString, Name: "author", Description: "Author name"},
				{Type: discordgo.ApplicationCommandOptionString, Name: "author-icon", Description: "Author icon URL"},
				{Type: discordgo.ApplicationCommandOptionString, Name: "url", Description: "Title hyperlink URL"},
			},
		},
	}
}

func handleSay(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isAdmin(s, i) {
		respond(s, i, "You need admin permissions to use this command.", true)
		return
	}

	opts := optionMap(i)
	ch := opts["channel"].ChannelValue(s)
	msg := opts["message"].StringValue()

	msg = strings.ReplaceAll(msg, "\\n", "\n")

	_, err := s.ChannelMessageSend(ch.ID, msg)
	if err != nil {
		respond(s, i, fmt.Sprintf("Failed to send message: %v", err), true)
		return
	}

	respond(s, i, fmt.Sprintf("Message sent to <#%s>!", ch.ID), true)
}

func handleEmbed(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isAdmin(s, i) {
		respond(s, i, "You need admin permissions to use this command.", true)
		return
	}

	opts := optionMap(i)
	ch := opts["channel"].ChannelValue(s)
	title := opts["title"].StringValue()
	desc := opts["description"].StringValue()

	desc = strings.ReplaceAll(desc, "\\n", "\n")
	title = strings.ReplaceAll(title, "\\n", "\n")

	colour := 0
	if c, ok := opts["colour"]; ok {
		hex := strings.TrimPrefix(c.StringValue(), "#")
		if v, err := strconv.ParseInt(hex, 16, 64); err == nil {
			colour = int(v)
		}
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: desc,
		Color:       colour,
	}

	if img, ok := opts["image"]; ok {
		embed.Image = &discordgo.MessageEmbedImage{URL: img.StringValue()}
	}
	if thumb, ok := opts["thumbnail"]; ok {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: thumb.StringValue()}
	}
	if footer, ok := opts["footer"]; ok {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: footer.StringValue()}
	}
	if u, ok := opts["url"]; ok {
		embed.URL = u.StringValue()
	}

	authorName := ""
	authorIcon := ""
	if a, ok := opts["author"]; ok {
		authorName = a.StringValue()
	}
	if ai, ok := opts["author-icon"]; ok {
		authorIcon = ai.StringValue()
	}
	if authorName != "" {
		embed.Author = &discordgo.MessageEmbedAuthor{Name: authorName, IconURL: authorIcon}
	}

	_, err := s.ChannelMessageSendEmbed(ch.ID, embed)
	if err != nil {
		respond(s, i, fmt.Sprintf("Failed to send embed: %v", err), true)
		return
	}

	respond(s, i, fmt.Sprintf("Embed sent to <#%s>!", ch.ID), true)
}
