package handlers

import (
	"fmt"
	"strings"
	"time"

	"discord-bot/config"
	"discord-bot/lang"
	"discord-bot/storage"

	"github.com/bwmarrin/discordgo"
)

func ticketCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:                     "ticket",
			Description:              "Ticket system management",
			DefaultMemberPermissions: &adminPerm,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name: "setup", Description: "Set up or update the ticket system (overrides config.json values)",
					Type: discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Channel to post the ticket panel in", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "staff-roles", Description: "Staff role ID(s), comma-separated", Required: true},
						{Type: discordgo.ApplicationCommandOptionChannel, Name: "log-channel", Description: "Channel for ticket logs"},
						{Type: discordgo.ApplicationCommandOptionChannel, Name: "category", Description: "Discord category for ticket channels"},
					},
				},
				{
					Name: "addcategory", Description: "Add a ticket category (in addition to config.json ones)",
					Type: discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "id", Description: "Short identifier (e.g. sales)", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "name", Description: "Display name", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "emoji", Description: "Emoji (e.g. 🎫)", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "description", Description: "Short description", Required: true},
					},
				},
				{
					Name: "removecategory", Description: "Remove a runtime-added ticket category",
					Type: discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "id", Description: "Category ID to remove", Required: true},
					},
				},
				{
					Name: "addsubcategory", Description: "Add a subcategory under an existing category",
					Type: discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "category-id", Description: "Parent category ID", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "id", Description: "Subcategory ID", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "name", Description: "Display name", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "emoji", Description: "Emoji", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "description", Description: "Short description", Required: true},
					},
				},
				{
					Name: "removesubcategory", Description: "Remove a subcategory",
					Type: discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "category-id", Description: "Parent category ID", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "id", Description: "Subcategory ID to remove", Required: true},
					},
				},
				{
					Name: "panel", Description: "Send or refresh the ticket panel",
					Type: discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name: "list", Description: "List all open tickets",
					Type: discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name: "config", Description: "Show the current ticket configuration",
					Type: discordgo.ApplicationCommandOptionSubCommand,
				},
			},
		},
		{Name: "close", Description: "Close the current ticket"},
		{
			Name: "add", Description: "Add a user to the current ticket",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to add", Required: true},
			},
		},
		{
			Name: "remove", Description: "Remove a user from the current ticket",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to remove", Required: true},
			},
		},
	}
}

func handleTicketCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	sub := i.ApplicationCommandData().Options[0]
	switch sub.Name {
	case "setup":
		handleTicketSetup(s, i, sub.Options)
	case "addcategory":
		handleTicketAddCategory(s, i, sub.Options)
	case "removecategory":
		handleTicketRemoveCategory(s, i, sub.Options)
	case "addsubcategory":
		handleTicketAddSubcategory(s, i, sub.Options)
	case "removesubcategory":
		handleTicketRemoveSubcategory(s, i, sub.Options)
	case "panel":
		handleTicketPanel(s, i)
	case "list":
		handleTicketList(s, i)
	case "config":
		handleTicketConfigCmd(s, i)
	}
}

func handleTicketSetup(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	gs := storage.GetGuild(i.GuildID)

	gs.Lock()
	gs.TicketRuntime.PanelChannelOverride = om["channel"].ChannelValue(s).ID
	gs.TicketRuntime.StaffRolesOverride = om["staff-roles"].StringValue()
	if lc, ok := om["log-channel"]; ok {
		gs.TicketRuntime.LogChannelOverride = lc.ChannelValue(s).ID
	}
	if cat, ok := om["category"]; ok {
		gs.TicketRuntime.DiscordCategoryOverride = cat.ChannelValue(s).ID
	}
	gs.Unlock()
	_ = gs.Save()

	respond(s, i, lang.T("ticket_setup_done"), true)
}

func handleTicketAddCategory(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	gs := storage.GetGuild(i.GuildID)

	cat := config.TicketCategory{
		ID:          om["id"].StringValue(),
		Name:        om["name"].StringValue(),
		Emoji:       om["emoji"].StringValue(),
		Description: om["description"].StringValue(),
	}

	gs.Lock()
	gs.TicketRuntime.ExtraCategories = append(gs.TicketRuntime.ExtraCategories, cat)
	gs.Unlock()
	_ = gs.Save()

	respond(s, i, lang.T("ticket_category_added", "emoji", cat.Emoji, "name", cat.Name), true)
}

func handleTicketRemoveCategory(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	id := om["id"].StringValue()
	gs := storage.GetGuild(i.GuildID)

	gs.Lock()
	found := false
	extras := gs.TicketRuntime.ExtraCategories
	for idx, c := range extras {
		if c.ID == id {
			gs.TicketRuntime.ExtraCategories = append(extras[:idx], extras[idx+1:]...)
			found = true
			break
		}
	}
	gs.Unlock()
	_ = gs.Save()

	if !found {
		respond(s, i, lang.T("ticket_category_not_found_runtime", "id", id), true)
		return
	}
	respond(s, i, lang.T("ticket_category_removed", "id", id), true)
}

func handleTicketAddSubcategory(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	catID := om["category-id"].StringValue()
	gs := storage.GetGuild(i.GuildID)

	sub := config.TicketSubcategory{
		ID:          om["id"].StringValue(),
		Name:        om["name"].StringValue(),
		Emoji:       om["emoji"].StringValue(),
		Description: om["description"].StringValue(),
	}

	gs.Lock()
	found := false
	for idx := range gs.TicketRuntime.ExtraCategories {
		if gs.TicketRuntime.ExtraCategories[idx].ID == catID {
			gs.TicketRuntime.ExtraCategories[idx].Subcategories = append(gs.TicketRuntime.ExtraCategories[idx].Subcategories, sub)
			found = true
			break
		}
	}
	gs.Unlock()

	if !found {
		for _, c := range storage.Cfg.Tickets.Categories {
			if c.ID == catID {
				respond(s, i, lang.T("ticket_category_config_only", "id", catID), true)
				return
			}
		}
		respond(s, i, lang.T("ticket_category_not_found", "id", catID), true)
		return
	}

	_ = gs.Save()
	respond(s, i, lang.T("ticket_subcategory_added", "emoji", sub.Emoji, "name", sub.Name, "parent", catID), true)
}

func handleTicketRemoveSubcategory(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	catID := om["category-id"].StringValue()
	subID := om["id"].StringValue()
	gs := storage.GetGuild(i.GuildID)

	gs.Lock()
	found := false
	for ci := range gs.TicketRuntime.ExtraCategories {
		if gs.TicketRuntime.ExtraCategories[ci].ID == catID {
			subs := gs.TicketRuntime.ExtraCategories[ci].Subcategories
			for si, sc := range subs {
				if sc.ID == subID {
					gs.TicketRuntime.ExtraCategories[ci].Subcategories = append(subs[:si], subs[si+1:]...)
					found = true
					break
				}
			}
			break
		}
	}
	gs.Unlock()
	_ = gs.Save()

	if !found {
		respond(s, i, lang.T("ticket_subcategory_not_found"), true)
		return
	}
	respond(s, i, lang.T("ticket_subcategory_removed", "id", subID), true)
}

func handleTicketPanel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cfg := storage.Cfg
	gs := storage.GetGuild(i.GuildID)

	panelCh := config.EffectiveTicketPanelChannel(cfg, gs)
	if panelCh == "" {
		respond(s, i, lang.T("ticket_no_panel_channel"), true)
		return
	}

	categories := config.MergedTicketCategories(cfg, gs)
	if len(categories) == 0 {
		respond(s, i, lang.T("ticket_no_categories"), true)
		return
	}

	var desc strings.Builder
	desc.WriteString(lang.T("ticket_panel_description") + "\n\n")
	for _, cat := range categories {
		desc.WriteString(fmt.Sprintf("%s **%s** — %s\n", cat.Emoji, cat.Name, cat.Description))
		for _, sub := range cat.Subcategories {
			desc.WriteString(fmt.Sprintf("   ↳ %s %s — %s\n", sub.Emoji, sub.Name, sub.Description))
		}
		desc.WriteString("\n")
	}

	embed := &discordgo.MessageEmbed{
		Title:       lang.T("ticket_panel_title"),
		Description: desc.String(),
		Color:       0x5865F2,
		Footer:      &discordgo.MessageEmbedFooter{Text: "Click the menu below to open a ticket"},
	}

	menuOpts := make([]discordgo.SelectMenuOption, 0, len(categories))
	for _, cat := range categories {
		menuOpts = append(menuOpts, discordgo.SelectMenuOption{
			Label:       cat.Name,
			Value:       cat.ID,
			Description: cat.Description,
			Emoji:       parseComponentEmoji(cat.Emoji),
		})
	}

	msg, err := s.ChannelMessageSendComplex(panelCh, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{
						MenuType:    discordgo.StringSelectMenu,
						CustomID:    "ticket_category_select",
						Placeholder: "Select a category...",
						Options:     menuOpts,
					},
				},
			},
		},
	})
	if err != nil {
		respond(s, i, lang.T("ticket_panel_send_failed", "error", err.Error()), true)
		return
	}

	if gs.TicketRuntime.PanelMessageID != "" {
		_ = s.ChannelMessageDelete(panelCh, gs.TicketRuntime.PanelMessageID)
	}

	gs.Lock()
	gs.TicketRuntime.PanelMessageID = msg.ID
	gs.Unlock()
	_ = gs.Save()

	respond(s, i, lang.T("ticket_panel_posted"), true)
}

func handleTicketList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	gs := storage.GetGuild(i.GuildID)
	tickets := gs.TicketRuntime.OpenTickets

	if len(tickets) == 0 {
		respond(s, i, lang.T("ticket_no_open"), true)
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Open Tickets** (%d):\n", len(tickets)))
	for _, t := range tickets {
		sub := t.SubCategory
		if sub == "" {
			sub = "—"
		}
		sb.WriteString(fmt.Sprintf("• <#%s> — #%d by <@%s> [%s / %s]\n", t.ChannelID, t.Number, t.UserID, t.CategoryID, sub))
	}
	respond(s, i, sb.String(), true)
}

func handleTicketConfigCmd(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cfg := storage.Cfg
	gs := storage.GetGuild(i.GuildID)
	categories := config.MergedTicketCategories(cfg, gs)

	var sb strings.Builder
	sb.WriteString("**Ticket System Configuration**\n\n")
	sb.WriteString("__From config.json:__\n")
	sb.WriteString(fmt.Sprintf("Enabled: `%v`\n", cfg.Tickets.Enabled))
	sb.WriteString(fmt.Sprintf("Panel Channel: `%s`\n", cfg.Tickets.PanelChannel))
	sb.WriteString(fmt.Sprintf("Log Channel: `%s`\n", cfg.Tickets.LogChannel))
	sb.WriteString(fmt.Sprintf("Staff Roles: `%s`\n", cfg.Tickets.StaffRoles))
	sb.WriteString(fmt.Sprintf("Discord Category: `%s`\n", cfg.Tickets.DiscordCategory))
	sb.WriteString(fmt.Sprintf("Max Open Per User: `%d`\n", cfg.Tickets.MaxOpenPerUser))
	sb.WriteString(fmt.Sprintf("Config Categories: `%d`\n\n", len(cfg.Tickets.Categories)))
	sb.WriteString("__Runtime Overrides:__\n")
	sb.WriteString(fmt.Sprintf("Panel Channel: `%s`\n", gs.TicketRuntime.PanelChannelOverride))
	sb.WriteString(fmt.Sprintf("Log Channel: `%s`\n", gs.TicketRuntime.LogChannelOverride))
	sb.WriteString(fmt.Sprintf("Staff Roles: `%s`\n", gs.TicketRuntime.StaffRolesOverride))
	sb.WriteString(fmt.Sprintf("Extra Categories: `%d`\n", len(gs.TicketRuntime.ExtraCategories)))
	sb.WriteString(fmt.Sprintf("Open Tickets: `%d`\n\n", len(gs.TicketRuntime.OpenTickets)))
	sb.WriteString("__Effective (merged) Categories:__\n")
	for _, cat := range categories {
		staffInfo := ""
		if cat.StaffRoles != "" {
			staffInfo = fmt.Sprintf(" [staff: `%s`]", cat.StaffRoles)
		}
		sb.WriteString(fmt.Sprintf("• %s **%s** (`%s`)%s\n", cat.Emoji, cat.Name, cat.ID, staffInfo))
		for _, sub := range cat.Subcategories {
			sb.WriteString(fmt.Sprintf("   ↳ %s %s (`%s`)\n", sub.Emoji, sub.Name, sub.ID))
		}
	}
	respond(s, i, sb.String(), true)
}

func handleTicketCategorySelect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	if len(data.Values) == 0 {
		return
	}
	catID := data.Values[0]
	cfg := storage.Cfg
	gs := storage.GetGuild(i.GuildID)
	categories := config.MergedTicketCategories(cfg, gs)

	var cat *config.TicketCategory
	for idx := range categories {
		if categories[idx].ID == catID {
			cat = &categories[idx]
			break
		}
	}
	if cat == nil {
		respond(s, i, lang.T("ticket_category_select_not_found"), true)
		return
	}

	if len(cat.Subcategories) > 0 {
		opts := make([]discordgo.SelectMenuOption, 0, len(cat.Subcategories))
		for _, sub := range cat.Subcategories {
			opts = append(opts, discordgo.SelectMenuOption{
				Label:       sub.Name,
				Value:       catID + ":" + sub.ID,
				Description: sub.Description,
				Emoji:       parseComponentEmoji(sub.Emoji),
			})
		}

		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("%s **%s** — Please select a more specific topic:", cat.Emoji, cat.Name),
				Flags:   discordgo.MessageFlagsEphemeral,
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{
						Components: []discordgo.MessageComponent{
							discordgo.SelectMenu{
								MenuType:    discordgo.StringSelectMenu,
								CustomID:    "ticket_subcategory_select",
								Placeholder: "Select a subcategory...",
								Options:     opts,
							},
						},
					},
				},
			},
		})
		return
	}

	createTicket(s, i, catID, "")
}

func handleTicketSubcategorySelect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	if len(data.Values) == 0 {
		return
	}
	parts := strings.SplitN(data.Values[0], ":", 2)
	catID := parts[0]
	subID := ""
	if len(parts) > 1 {
		subID = parts[1]
	}

	createTicket(s, i, catID, subID)
}

func createTicket(s *discordgo.Session, i *discordgo.InteractionCreate, catID, subID string) {
	cfg := storage.Cfg
	gs := storage.GetGuild(i.GuildID)
	userID := i.Member.User.ID

	maxOpen := cfg.Tickets.MaxOpenPerUser
	if maxOpen <= 0 {
		maxOpen = 1
	}
	openCount := 0
	for _, t := range gs.TicketRuntime.OpenTickets {
		if t.UserID == userID {
			openCount++
		}
	}
	if openCount >= maxOpen {
		respond(s, i, lang.T("ticket_max_open",
			"count", fmt.Sprintf("%d", openCount),
			"max", fmt.Sprintf("%d", maxOpen),
		), true)
		return
	}

	gs.Lock()
	gs.TicketRuntime.TicketCounter++
	num := gs.TicketRuntime.TicketCounter
	gs.Unlock()

	channelName := fmt.Sprintf("ticket-%04d", num)
	discordCat := config.EffectiveTicketCategory(cfg, gs)

	globalRoles := config.EffectiveTicketStaffRoles(cfg, gs)
	categories := config.MergedTicketCategories(cfg, gs)
	var staffRoles []string
	for idx := range categories {
		if categories[idx].ID == catID {
			staffRoles = config.CategoryStaffRoles(&categories[idx], globalRoles)
			break
		}
	}
	if len(staffRoles) == 0 {
		staffRoles = globalRoles
	}

	overwrites := []*discordgo.PermissionOverwrite{
		{ID: i.GuildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
		{
			ID:    userID,
			Type:  discordgo.PermissionOverwriteTypeMember,
			Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionAttachFiles | discordgo.PermissionReadMessageHistory,
		},
	}
	for _, roleID := range staffRoles {
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:    roleID,
			Type:  discordgo.PermissionOverwriteTypeRole,
			Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionAttachFiles | discordgo.PermissionReadMessageHistory | discordgo.PermissionManageMessages,
		})
	}

	ch, err := s.GuildChannelCreateComplex(i.GuildID, discordgo.GuildChannelCreateData{
		Name:                 channelName,
		Type:                 discordgo.ChannelTypeGuildText,
		ParentID:             discordCat,
		PermissionOverwrites: overwrites,
	})
	if err != nil {
		respond(s, i, lang.T("ticket_create_failed", "error", err.Error()), true)
		return
	}

	catName := catID
	subName := subID
	for _, c := range categories {
		if c.ID == catID {
			catName = c.Emoji + " " + c.Name
			for _, sc := range c.Subcategories {
				if sc.ID == subID {
					subName = sc.Emoji + " " + sc.Name
				}
			}
		}
	}

	topicText := catName
	if subName != "" && subName != subID {
		topicText += " → " + subName
	}

	ticket := config.Ticket{
		ChannelID:   ch.ID,
		UserID:      userID,
		CategoryID:  catID,
		SubCategory: subID,
		Number:      num,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}
	gs.Lock()
	gs.TicketRuntime.OpenTickets[ch.ID] = ticket
	gs.Unlock()
	_ = gs.Save()

	embed := &discordgo.MessageEmbed{
		Title:       lang.T("ticket_welcome_title", "number", fmt.Sprintf("%04d", num)),
		Description: lang.T("ticket_welcome_body", "user_id", userID, "category", topicText),
		Color:       0x57F287,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	pingContent := fmt.Sprintf("<@%s>", userID)
	for _, roleID := range staffRoles {
		pingContent += fmt.Sprintf(" | <@&%s>", roleID)
	}

	_, _ = s.ChannelMessageSendComplex(ch.ID, &discordgo.MessageSend{
		Content: pingContent,
		Embeds:  []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    lang.T("ticket_close_btn_label"),
						Style:    discordgo.DangerButton,
						CustomID: "ticket_close_btn",
					},
				},
			},
		},
	})

	respond(s, i, lang.T("ticket_created", "channel_id", ch.ID), true)
}

func handleCloseCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	gs := storage.GetGuild(i.GuildID)
	ticket, ok := gs.TicketRuntime.OpenTickets[i.ChannelID]
	if !ok {
		respond(s, i, lang.T("ticket_not_ticket_channel"), true)
		return
	}

	closeTicket(s, i.GuildID, i.ChannelID, i.Member.User, &ticket, gs)
	respond(s, i, lang.T("ticket_closing"), false)
}

func handleCloseButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	gs := storage.GetGuild(i.GuildID)
	_, ok := gs.TicketRuntime.OpenTickets[i.ChannelID]
	if !ok {
		respond(s, i, lang.T("ticket_not_ticket_channel"), true)
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: lang.T("ticket_close_confirm_prompt"),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{Label: lang.T("ticket_close_confirm_btn"), Style: discordgo.DangerButton, CustomID: "ticket_close_confirm"},
						discordgo.Button{Label: lang.T("ticket_close_cancel_btn"), Style: discordgo.SecondaryButton, CustomID: "ticket_close_cancel"},
					},
				},
			},
		},
	})
}

func handleCloseConfirm(s *discordgo.Session, i *discordgo.InteractionCreate) {
	gs := storage.GetGuild(i.GuildID)
	ticket, ok := gs.TicketRuntime.OpenTickets[i.ChannelID]
	if !ok {
		respond(s, i, lang.T("ticket_not_found"), true)
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: "🔒 Closing ticket..."},
	})

	closeTicket(s, i.GuildID, i.ChannelID, i.Member.User, &ticket, gs)
}

func handleCloseCancel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    lang.T("ticket_close_cancelled"),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func closeTicket(s *discordgo.Session, guildID, channelID string, closedBy *discordgo.User, ticket *config.Ticket, gs *config.GuildState) {
	cfg := storage.Cfg
	transcript := generateTranscript(s, channelID)

	logCh := config.EffectiveTicketLogChannel(cfg, gs)
	if logCh != "" {
		embed := &discordgo.MessageEmbed{
			Title: fmt.Sprintf("Ticket #%04d Closed", ticket.Number),
			Color: 0xED4245,
			Fields: []*discordgo.MessageEmbedField{
				{Name: "Opened By", Value: fmt.Sprintf("<@%s>", ticket.UserID), Inline: true},
				{Name: "Closed By", Value: fmt.Sprintf("<@%s>", closedBy.ID), Inline: true},
				{Name: "Category", Value: ticket.CategoryID, Inline: true},
				{Name: "Subcategory", Value: ticket.SubCategory, Inline: true},
				{Name: "Opened At", Value: ticket.CreatedAt, Inline: true},
			},
			Timestamp: time.Now().Format(time.RFC3339),
		}

		_, _ = s.ChannelMessageSendComplex(logCh, &discordgo.MessageSend{
			Embeds: []*discordgo.MessageEmbed{embed},
			Files: []*discordgo.File{
				{
					Name:        fmt.Sprintf("ticket-%04d-transcript.txt", ticket.Number),
					ContentType: "text/plain",
					Reader:      strings.NewReader(transcript),
				},
			},
		})
	}

	gs.Lock()
	delete(gs.TicketRuntime.OpenTickets, channelID)
	gs.Unlock()
	_ = gs.Save()

	time.Sleep(3 * time.Second)
	_, _ = s.ChannelDelete(channelID)
}

func handleAddUser(s *discordgo.Session, i *discordgo.InteractionCreate) {
	gs := storage.GetGuild(i.GuildID)
	if _, ok := gs.TicketRuntime.OpenTickets[i.ChannelID]; !ok {
		respond(s, i, lang.T("ticket_not_ticket_channel"), true)
		return
	}

	opts := optionMap(i)
	target := opts["user"].UserValue(s)

	err := s.ChannelPermissionSet(i.ChannelID, target.ID, discordgo.PermissionOverwriteTypeMember,
		discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionReadMessageHistory, 0)
	if err != nil {
		respond(s, i, lang.T("ticket_add_user_failed", "error", err.Error()), true)
		return
	}
	respond(s, i, lang.T("ticket_user_added", "user_id", target.ID), false)
}

func handleRemoveUser(s *discordgo.Session, i *discordgo.InteractionCreate) {
	gs := storage.GetGuild(i.GuildID)
	if _, ok := gs.TicketRuntime.OpenTickets[i.ChannelID]; !ok {
		respond(s, i, lang.T("ticket_not_ticket_channel"), true)
		return
	}

	opts := optionMap(i)
	target := opts["user"].UserValue(s)

	err := s.ChannelPermissionDelete(i.ChannelID, target.ID)
	if err != nil {
		respond(s, i, lang.T("ticket_remove_user_failed", "error", err.Error()), true)
		return
	}
	respond(s, i, lang.T("ticket_user_removed", "user_id", target.ID), false)
}

func generateTranscript(s *discordgo.Session, channelID string) string {
	var sb strings.Builder
	sb.WriteString("=== TICKET TRANSCRIPT ===\n\n")

	msgs, err := s.ChannelMessages(channelID, 100, "", "", "")
	if err != nil {
		sb.WriteString("(Failed to fetch messages)\n")
		return sb.String()
	}

	for idx := len(msgs) - 1; idx >= 0; idx-- {
		m := msgs[idx]
		ts := m.Timestamp.Format("2006-01-02 15:04:05")
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", ts, m.Author.Username, m.Content))
		for _, a := range m.Attachments {
			sb.WriteString(fmt.Sprintf("  📎 %s\n", a.URL))
		}
	}
	return sb.String()
}

func parseComponentEmoji(emoji string) *discordgo.ComponentEmoji {
	if emoji == "" {
		return nil
	}
	return &discordgo.ComponentEmoji{Name: emoji}
}
