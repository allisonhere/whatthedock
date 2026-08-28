package clipboard

// defaultHistoryLimit is how many yanked items DeploymentClipboard keeps —
// "the last 5-10 yanked items" per the feature request. A plain in-memory
// ring; nothing here is persisted to disk (see the package doc for why:
// v1's clipboard only ever needs to survive a host switch within the same
// whatthedock session, which a Model field already gives it for free).
const defaultHistoryLimit = 8

// DeploymentClipboard is a small yank history: items[0] is always the most
// recently yanked container, items[1:] progressively older ones, capped at
// max. Value type with value-receiver methods that return an updated copy —
// matching internal/ui's own Model convention (mutate a local copy,
// reassign) rather than a pointer type, so it composes the same way every
// other piece of Model state does.
type DeploymentClipboard struct {
	items []PortableContainer
	max   int
}

// NewDeploymentClipboard returns an empty clipboard with the default
// history limit.
func NewDeploymentClipboard() DeploymentClipboard {
	return DeploymentClipboard{max: defaultHistoryLimit}
}

// Yank pushes pc to the front of the history, evicting the oldest entry
// once max is exceeded.
func (c DeploymentClipboard) Yank(pc PortableContainer) DeploymentClipboard {
	max := c.max
	if max <= 0 {
		max = defaultHistoryLimit
	}
	items := append([]PortableContainer{pc}, c.items...)
	if len(items) > max {
		items = items[:max]
	}
	return DeploymentClipboard{items: items, max: max}
}

// Current returns the most recently yanked item, or false if nothing has
// been yanked yet this session.
func (c DeploymentClipboard) Current() (PortableContainer, bool) {
	if len(c.items) == 0 {
		return PortableContainer{}, false
	}
	return c.items[0], true
}

// History returns every yanked item, most recent first.
func (c DeploymentClipboard) History() []PortableContainer {
	return append([]PortableContainer(nil), c.items...)
}

// Len reports how many items are currently held.
func (c DeploymentClipboard) Len() int { return len(c.items) }
