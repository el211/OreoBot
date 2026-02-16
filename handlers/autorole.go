package handlers

import (
	"fmt"
	"strings"

	"discord-bot/config"
	"discord-bot/storage"

	"github.com/bwmarrin/discordgo"
)

func autoroleCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:                     "joinrole",
			Description:              "Configure the role automatically given to new members",
			DefaultMemberPermissions: &adminPerm,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "set",
					Description: "Set the role given on join",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionRole, Name: "role", Description: "Role to assign when someone joins", Required: true},
					},
				},
				{
					Name:        "disable",
					Description: "Disable auto-role on join",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "status",
					Description: "Show the current join role configuration",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
			},
		},
		{
			Name:                     "rolemenu",
			Description:              "Create self-assignable role menus for members",
			DefaultMemberPermissions: &adminPerm,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "create",
					Description: "Create a new role menu",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "title", Description: "Menu title (e.g. 'Choose your gender')", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "description", Description: "Menu description"},
						{Type: discordgo.ApplicationCommandOptionBoolean, Name: "single", Description: "Only one role at a time — selecting one removes the others (default: false)"},
					},
				},
				{
					Name:        "add",
					Description: "Add a role button to an existing menu",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "menu_id", Description: "Menu ID (from /rolemenu list)", Required: true},
						{Type: discordgo.ApplicationCommandOptionRole, Name: "role", Description: "Role to add", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "label", Description: "Button label — include emoji here if you want (e.g. 🇫🇷 Français)", Required: true},
					},
				},
				{
					Name:        "post",
					Description: "Post the role menu in a channel so members can use it",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "menu_id", Description: "Menu ID to post", Required: true},
						{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Channel to post the menu in", Required: true},
					},
				},
				{
					Name:        "list",
					Description: "List all role menus",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "delete",
					Description: "Delete a role menu",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "menu_id", Description: "Menu ID to delete", Required: true},
					},
				},
			},
		},
	}
}

func handleJoinRoleCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isAdmin(s, i) {
		respond(s, i, "❌ You need admin permissions.", true)
		return
	}
	sub := i.ApplicationCommandData().Options[0]
	gs := storage.GetGuild(i.GuildID)

	switch sub.Name {
	case "set":
		om := subOptMap(sub.Options)
		role := om["role"].RoleValue(s, i.GuildID)
		gs.Lock()
		gs.AutoRole = config.AutoRoleState{Enabled: true, RoleID: role.ID}
		gs.Unlock()
		_ = gs.Save()
		respond(s, i, fmt.Sprintf("✅ Join role set to <@&%s>. Every new member will receive this role automatically.", role.ID), true)

	case "disable":
		gs.Lock()
		gs.AutoRole = config.AutoRoleState{Enabled: false}
		gs.Unlock()
		_ = gs.Save()
		respond(s, i, "✅ Auto-role on join has been **disabled**.", true)

	case "status":
		gs.Lock()
		ar := gs.AutoRole
		gs.Unlock()
		if ar.Enabled && ar.RoleID != "" {
			respond(s, i, fmt.Sprintf("🎭 Auto-role is **enabled**. New members receive: <@&%s>", ar.RoleID), true)
		} else {
			respond(s, i, "🎭 Auto-role on join is **disabled**.", true)
		}
	}
}

// AssignJoinRole is called from welcome.go when a member joins.
func AssignJoinRole(s *discordgo.Session, guildID, userID string) {
	gs := storage.GetGuild(guildID)
	gs.Lock()
	ar := gs.AutoRole
	gs.Unlock()

	if !ar.Enabled || ar.RoleID == "" {
		return
	}
	_ = s.GuildMemberRoleAdd(guildID, userID, ar.RoleID)
}

func handleRoleMenuCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isAdmin(s, i) {
		respond(s, i, "❌ You need admin permissions.", true)
		return
	}
	sub := i.ApplicationCommandData().Options[0]

	switch sub.Name {
	case "create":
		handleRoleMenuCreate(s, i, sub.Options)
	case "add":
		handleRoleMenuAdd(s, i, sub.Options)
	case "post":
		handleRoleMenuPost(s, i, sub.Options)
	case "list":
		handleRoleMenuList(s, i)
	case "delete":
		handleRoleMenuDelete(s, i, sub.Options)
	}
}

func handleRoleMenuCreate(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	title := om["title"].StringValue()
	desc := ""
	if d, ok := om["description"]; ok {
		desc = d.StringValue()
	}
	single := false
	if sg, ok := om["single"]; ok {
		single = sg.BoolValue()
	}

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	menuID := fmt.Sprintf("menu%d", len(gs.RoleMenus)+1)
	menu := config.RoleMenu{
		ID:           menuID,
		Title:        title,
		Description:  desc,
		SingleSelect: single,
		Roles:        []config.RoleMenuEntry{},
	}
	gs.RoleMenus = append(gs.RoleMenus, menu)
	gs.Unlock()
	_ = gs.Save()

	mode := "multi-select (members can hold multiple roles)"
	if single {
		mode = "single-select (selecting one removes the others)"
	}
	respond(s, i, fmt.Sprintf("✅ Role menu **%s** created! ID: `%s`\nMode: %s\n\nNext steps:\n• `/rolemenu add menu_id:%s role:... label:...` to add roles\n• `/rolemenu post menu_id:%s channel:#...` to publish it", title, menuID, mode, menuID, menuID), true)
}

func handleRoleMenuAdd(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	menuID := om["menu_id"].StringValue()
	role := om["role"].RoleValue(s, i.GuildID)
	label := om["label"].StringValue()

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	found := false
	for idx := range gs.RoleMenus {
		if gs.RoleMenus[idx].ID == menuID {
			if len(gs.RoleMenus[idx].Roles) >= 20 {
				gs.Unlock()
				respond(s, i, "❌ A menu can have at most 20 roles (Discord button limit).", true)
				return
			}
			gs.RoleMenus[idx].Roles = append(gs.RoleMenus[idx].Roles, config.RoleMenuEntry{
				RoleID: role.ID,
				Label:  label,
			})
			found = true
			break
		}
	}
	gs.Unlock()

	if !found {
		respond(s, i, fmt.Sprintf("❌ Menu `%s` not found. Use `/rolemenu list` to see available menus.", menuID), true)
		return
	}
	_ = gs.Save()
	respond(s, i, fmt.Sprintf("✅ Added **%s** (<@&%s>) to menu `%s`.", label, role.ID, menuID), true)
}

func handleRoleMenuPost(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	menuID := om["menu_id"].StringValue()
	ch := om["channel"].ChannelValue(s)

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()

	var menuCopy *config.RoleMenu
	for idx := range gs.RoleMenus {
		if gs.RoleMenus[idx].ID == menuID {
			cp := gs.RoleMenus[idx]
			menuCopy = &cp
			break
		}
	}
	if menuCopy == nil {
		gs.Unlock()
		respond(s, i, fmt.Sprintf("❌ Menu `%s` not found. Use `/rolemenu list`.", menuID), true)
		return
	}
	if len(menuCopy.Roles) == 0 {
		gs.Unlock()
		respond(s, i, "❌ This menu has no roles yet. Use `/rolemenu add` first.", true)
		return
	}
	gs.Unlock()

	embed := buildRoleMenuEmbed(menuCopy)
	components := buildRoleMenuComponents(menuCopy)

	msg, err := s.ChannelMessageSendComplex(ch.ID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
	})
	if err != nil {
		respond(s, i, fmt.Sprintf("❌ Failed to post menu: %v", err), true)
		return
	}

	gs.Lock()
	for idx := range gs.RoleMenus {
		if gs.RoleMenus[idx].ID == menuID {
			gs.RoleMenus[idx].ChannelID = ch.ID
			gs.RoleMenus[idx].MessageID = msg.ID
			break
		}
	}
	gs.Unlock()
	_ = gs.Save()

	respond(s, i, fmt.Sprintf("✅ Role menu **%s** posted in <#%s>!", menuCopy.Title, ch.ID), true)
}

func handleRoleMenuList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	menus := make([]config.RoleMenu, len(gs.RoleMenus))
	copy(menus, gs.RoleMenus)
	gs.Unlock()

	if len(menus) == 0 {
		respond(s, i, "📋 No role menus created yet. Use `/rolemenu create` to make one.", true)
		return
	}

	var sb strings.Builder
	sb.WriteString("📋 **Role Menus:**\n\n")
	for _, m := range menus {
		posted := "not posted yet"
		if m.MessageID != "" {
			posted = fmt.Sprintf("posted in <#%s>", m.ChannelID)
		}
		mode := "multi-select"
		if m.SingleSelect {
			mode = "single-select"
		}
		sb.WriteString(fmt.Sprintf("`%s` — **%s** | %d role(s) | %s | %s\n", m.ID, m.Title, len(m.Roles), mode, posted))
	}
	respond(s, i, sb.String(), true)
}

func handleRoleMenuDelete(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	menuID := om["menu_id"].StringValue()

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	found := false
	newMenus := make([]config.RoleMenu, 0, len(gs.RoleMenus))
	for _, m := range gs.RoleMenus {
		if m.ID == menuID {
			found = true
			continue
		}
		newMenus = append(newMenus, m)
	}
	gs.RoleMenus = newMenus
	gs.Unlock()

	if !found {
		respond(s, i, fmt.Sprintf("❌ Menu `%s` not found.", menuID), true)
		return
	}
	_ = gs.Save()
	respond(s, i, fmt.Sprintf("🗑️ Role menu `%s` deleted.", menuID), true)
}

func HandleRoleMenuButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.SplitN(i.MessageComponentData().CustomID, ":", 3)
	if len(parts) != 3 {
		return
	}
	menuID := parts[1]
	roleID := parts[2]
	userID := i.Member.User.ID

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	var menu *config.RoleMenu
	for idx := range gs.RoleMenus {
		if gs.RoleMenus[idx].ID == menuID {
			menu = &gs.RoleMenus[idx]
			break
		}
	}
	if menu == nil {
		gs.Unlock()
		respond(s, i, "❌ This role menu no longer exists.", true)
		return
	}
	isSingle := menu.SingleSelect
	menuRoleIDs := make([]string, len(menu.Roles))
	for idx, r := range menu.Roles {
		menuRoleIDs[idx] = r.RoleID
	}
	gs.Unlock()

	member, err := s.GuildMember(i.GuildID, userID)
	if err != nil {
		respond(s, i, "❌ Could not fetch your member data.", true)
		return
	}

	hasRole := false
	for _, rid := range member.Roles {
		if rid == roleID {
			hasRole = true
			break
		}
	}

	if hasRole {
		_ = s.GuildMemberRoleRemove(i.GuildID, userID, roleID)
		respond(s, i, fmt.Sprintf("✅ Removed <@&%s> from you.", roleID), true)
	} else {
		if isSingle {
			for _, rid := range menuRoleIDs {
				if rid != roleID {
					_ = s.GuildMemberRoleRemove(i.GuildID, userID, rid)
				}
			}
		}
		_ = s.GuildMemberRoleAdd(i.GuildID, userID, roleID)
		respond(s, i, fmt.Sprintf("✅ You now have <@&%s>! Click again to remove it.", roleID), true)
	}
}

func buildRoleMenuEmbed(menu *config.RoleMenu) *discordgo.MessageEmbed {
	desc := menu.Description
	if desc == "" {
		desc = "Click a button below to get or remove a role."
	}
	if menu.SingleSelect {
		desc += "\n\n> ℹ️ **Single-select:** Choosing one role will remove your current selection."
	} else {
		desc += "\n\n> ℹ️ **Multi-select:** You can pick multiple roles. Click a role again to remove it."
	}

	return &discordgo.MessageEmbed{
		Title:       "🎭 " + menu.Title,
		Description: desc,
		Color:       0x5865F2,
	}
}

func buildRoleMenuComponents(menu *config.RoleMenu) []discordgo.MessageComponent {
	var rows []discordgo.MessageComponent
	var currentRow []discordgo.MessageComponent

	for idx, r := range menu.Roles {
		btn := discordgo.Button{
			Label:    r.Label,
			Style:    discordgo.PrimaryButton,
			CustomID: fmt.Sprintf("rolemenu:%s:%s", menu.ID, r.RoleID),
		}
		currentRow = append(currentRow, btn)

		if len(currentRow) == 5 || idx == len(menu.Roles)-1 {
			rows = append(rows, discordgo.ActionsRow{Components: currentRow})
			currentRow = nil
		}
	}

	return rows
}
