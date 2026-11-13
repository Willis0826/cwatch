//go:build darwin && cgo

package menubar

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>

#import <Cocoa/Cocoa.h>

extern void cwatchMenuEvent(int kind, char *id);

// The events are the same as menuEvent kinds in Go.
enum { eventOpened = 0, eventFocus = 1, eventDashboard = 2 };

static const CGFloat menuWidth = 360;
static const CGFloat pad = 12;
static const CGFloat cardHeight = 52;
static const CGFloat cardGap = 6;
static const int maxVisibleCards = 7;
static const CGFloat headerTop = 12;

static NSColor *rgb(unsigned hex) {
	return [NSColor colorWithSRGBRed:((hex >> 16) & 0xff) / 255.0
		green:((hex >> 8) & 0xff) / 255.0 blue:(hex & 0xff) / 255.0 alpha:1];
}

// The brand colours come from the README banner.
static NSColor *colBackground(void) { return rgb(0x011835); }
static NSColor *colCard(void) { return rgb(0x0A2547); }
static NSColor *colCardHover(void) { return rgb(0x123260); }
static NSColor *colLine(void) { return rgb(0x1C3A66); }
static NSColor *colMint(void) { return rgb(0x44EABD); }
static NSColor *colText(void) { return rgb(0xE6EDF7); }
static NSColor *colFaint(void) { return rgb(0x8FA3C0); }
static NSColor *colBranch(void) { return rgb(0x7FB3FF); }

// stateColor returns the colour of a state. The colours are the same as in
// the dashboard.
static NSColor *stateColor(NSString *st) {
	if ([st isEqual:@"needs_permission"]) return rgb(0xFFC857);
	if ([st isEqual:@"error"]) return rgb(0xFF6B6B);
	if ([st isEqual:@"working"]) return rgb(0x6BD68A);
	if ([st isEqual:@"running"]) return rgb(0x7FB3FF);
	return rgb(0xA3ACC8);
}

// ---------------------------------------------------------------------------
// The owl in the menu bar. It is pixel art like the owl in the banner. Each
// string is one row; "#" is a filled pixel. The body is an outline and the
// eyes are filled, so that the eyes look bright, with dark pupils.

static const char *owlSleep[16] = {
	"...#........#...",
	"...##......##...",
	"..#..######..#..",
	".#............#.",
	".#............#.",
	"#..............#",
	"#.#...#..#...#.#",
	"#..###....###..#",
	"#..............#",
	"#..............#",
	"#......##......#",
	"#.#....##....#.#",
	"#.#..........#.#",
	".#.#........#.#.",
	"..#..........#..",
	"...###.##.###...",
};

static const char *owlAwake[16] = {
	"...#........#...",
	"...##......##...",
	"..#..######..#..",
	".#............#.",
	".#.####..####.#.",
	"#.############.#",
	"#.##..####..##.#",
	"#.##..####..##.#",
	"#.############.#",
	"#..####..####..#",
	"#......##......#",
	"#.#....##....#.#",
	"#.#..........#.#",
	".#.#........#.#.",
	"..#..........#..",
	"...###.##.###...",
};

static NSImage *pixelImage(const char **rows, NSColor *color) {
	return [NSImage imageWithSize:NSMakeSize(16, 16) flipped:YES drawingHandler:^BOOL(NSRect r) {
		[color setFill];
		for (int y = 0; y < 16; y++) {
			for (int x = 0; x < 16; x++) {
				if (rows[y][x] == '#') {
					NSRectFillUsingOperation(NSMakeRect(x, y, 1, 1), NSCompositingOperationSourceOver);
				}
			}
		}
		return YES;
	}];
}

static NSImage *owlImage(NSString *mood) {
	if ([mood isEqual:@"alert"]) {
		// A yellow owl is easy to see. It is not a template image, so macOS
		// keeps the colour.
		return pixelImage(owlAwake, NSColor.systemYellowColor);
	}
	NSImage *img = pixelImage([mood isEqual:@"awake"] ? owlAwake : owlSleep, NSColor.blackColor);
	img.template = YES;
	return img;
}

static NSImage *dotImage(NSColor *color, CGFloat d) {
	return [NSImage imageWithSize:NSMakeSize(d, d) flipped:NO drawingHandler:^BOOL(NSRect r) {
		[color setFill];
		[[NSBezierPath bezierPathWithOvalInRect:r] fill];
		return YES;
	}];
}

// ---------------------------------------------------------------------------
// Small views.

@interface CWFlippedView : NSView
@end

@implementation CWFlippedView
- (BOOL)isFlipped { return YES; }
@end

static NSTextField *textLabel(NSString *s, CGFloat size, NSFontWeight weight, NSColor *color) {
	NSTextField *l = [NSTextField labelWithString:s ?: @""];
	l.font = [NSFont systemFontOfSize:size weight:weight];
	l.textColor = color;
	l.lineBreakMode = NSLineBreakByTruncatingTail;
	l.maximumNumberOfLines = 1;
	[l sizeToFit];
	return l;
}

static NSView *filledView(NSRect frame, NSColor *color, CGFloat radius) {
	NSView *v = [[NSView alloc] initWithFrame:frame];
	v.wantsLayer = YES;
	v.layer.backgroundColor = color.CGColor;
	v.layer.cornerRadius = radius;
	return v;
}

// CWCard is one session. A click on the card focuses the pane.
@interface CWCard : CWFlippedView
@property (copy) NSString *sessionID;
@property (strong) NSColor *fill;
@property (nonatomic, assign) BOOL hover;
@end

@implementation CWCard

- (void)updateTrackingAreas {
	[super updateTrackingAreas];
	for (NSTrackingArea *a in self.trackingAreas) {
		[self removeTrackingArea:a];
	}
	[self addTrackingArea:[[NSTrackingArea alloc] initWithRect:NSZeroRect
		options:NSTrackingMouseEnteredAndExited | NSTrackingActiveAlways | NSTrackingInVisibleRect
		owner:self userInfo:nil]];
}

- (void)setHover:(BOOL)hover {
	_hover = hover;
	self.layer.backgroundColor = (hover ? colCardHover() : self.fill).CGColor;
}

- (void)mouseEntered:(NSEvent *)e { self.hover = YES; }
- (void)mouseExited:(NSEvent *)e { self.hover = NO; }

// The labels on the card do not take the clicks.
- (NSView *)hitTest:(NSPoint)p {
	return NSPointInRect([self convertPoint:p fromView:self.superview], self.bounds) ? self : nil;
}

- (void)mouseDown:(NSEvent *)e {}

- (void)mouseUp:(NSEvent *)e {
	if (NSPointInRect([self convertPoint:e.locationInWindow fromView:nil], self.bounds)) {
		cwatchMenuEvent(eventFocus, (char *)self.sessionID.UTF8String);
	}
}

@end

// ---------------------------------------------------------------------------
// The menu: the status item and the panel that opens below it.

@interface CWMenu : NSObject
@property (strong) NSStatusItem *item;
@property (strong) NSPanel *panel;
@property (strong) NSDictionary *data;
@property (strong) id clickMonitor;
@property (strong) NSScrollView *scroll;
@end

@implementation CWMenu

- (void)setup {
	self.item = [NSStatusBar.systemStatusBar statusItemWithLength:NSVariableStatusItemLength];
	NSStatusBarButton *b = self.item.button;
	b.imagePosition = NSImageLeft;
	b.target = self;
	b.action = @selector(toggle:);
	[b sendActionOn:NSEventMaskLeftMouseDown | NSEventMaskRightMouseDown];
	[self update:@{@"mood": @"sleep", @"title": @"cwatch", @"counts": @[], @"rows": @[]}];
}

- (void)update:(NSDictionary *)d {
	self.data = d;
	NSStatusBarButton *b = self.item.button;
	b.image = owlImage(d[@"mood"]);
	b.toolTip = d[@"title"];
	b.accessibilityLabel = d[@"title"];

	// The title is a coloured dot and a number for each state.
	NSFont *font = [NSFont monospacedDigitSystemFontOfSize:0 weight:NSFontWeightMedium];
	NSMutableAttributedString *s = [NSMutableAttributedString new];
	for (NSDictionary *c in d[@"counts"]) {
		NSTextAttachment *dot = [NSTextAttachment new];
		dot.image = dotImage(stateColor(c[@"state"]), 8);
		dot.bounds = NSMakeRect(0, 0.5, 8, 8);
		[s appendAttributedString:[[NSAttributedString alloc] initWithString:@" "]];
		[s appendAttributedString:[NSAttributedString attributedStringWithAttachment:dot]];
		[s appendAttributedString:[[NSAttributedString alloc] initWithString:[NSString stringWithFormat:@" %@", c[@"n"]]
			attributes:@{NSFontAttributeName: font, NSForegroundColorAttributeName: NSColor.labelColor}]];
	}
	b.attributedTitle = s;

	if (self.panel.visible) {
		[self layoutPanel];
	}
}

- (void)toggle:(id)sender {
	if (self.panel.visible) {
		[self close];
	} else {
		[self open];
	}
}

- (void)buildPanel {
	NSPanel *p = [[NSPanel alloc] initWithContentRect:NSMakeRect(0, 0, menuWidth, 200)
		styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
		backing:NSBackingStoreBuffered defer:YES];
	p.level = NSPopUpMenuWindowLevel;
	p.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces |
		NSWindowCollectionBehaviorFullScreenAuxiliary | NSWindowCollectionBehaviorIgnoresCycle;
	p.opaque = NO;
	p.backgroundColor = NSColor.clearColor;
	p.hasShadow = YES;
	p.hidesOnDeactivate = NO;
	p.releasedWhenClosed = NO;
	p.floatingPanel = YES;
	p.becomesKeyOnlyIfNeeded = YES;
	// The brand style is dark, so the controls must also be dark.
	p.appearance = [NSAppearance appearanceNamed:NSAppearanceNameDarkAqua];

	CWFlippedView *root = [[CWFlippedView alloc] initWithFrame:p.contentView.frame];
	root.wantsLayer = YES;
	root.layer.backgroundColor = colBackground().CGColor;
	root.layer.cornerRadius = 12;
	root.layer.masksToBounds = YES;
	root.layer.borderWidth = 1;
	root.layer.borderColor = colLine().CGColor;
	p.contentView = root;
	self.panel = p;
}

- (void)open {
	if (!self.panel) {
		[self buildPanel];
	}
	[self layoutPanel];
	self.panel.alphaValue = 0;
	[self.panel orderFrontRegardless];
	[NSAnimationContext runAnimationGroup:^(NSAnimationContext *ctx) {
		ctx.duration = 0.12;
		self.panel.animator.alphaValue = 1;
	}];
	[self.item.button highlight:YES];
	// A click in another app or on the desktop closes the panel.
	__weak CWMenu *weakSelf = self;
	self.clickMonitor = [NSEvent addGlobalMonitorForEventsMatchingMask:NSEventMaskLeftMouseDown | NSEventMaskRightMouseDown
		handler:^(NSEvent *e) { [weakSelf close]; }];
	cwatchMenuEvent(eventOpened, "");
}

- (void)close {
	if (self.clickMonitor) {
		[NSEvent removeMonitor:self.clickMonitor];
		self.clickMonitor = nil;
	}
	[self.item.button highlight:NO];
	[self.panel orderOut:nil];
}

// layoutPanel makes the content of the panel again from the data.
- (void)layoutPanel {
	NSView *root = self.panel.contentView;
	NSPoint scrollAt = self.scroll.contentView.bounds.origin;
	for (NSView *v in [root.subviews copy]) {
		[v removeFromSuperview];
	}
	NSDictionary *d = self.data;
	CGFloat inner = menuWidth - 2 * pad;
	CGFloat y = 0;

	// The header: a chip for each state. When no session is live, the
	// status is in the centre.
	NSArray *counts = d[@"counts"];
	NSString *notice = d[@"notice"];
	NSArray *rows = d[@"rows"];
	BOOL empty = rows.count == 0;
	y = headerTop;
	if (counts.count > 0) {
		CGFloat x = pad, chipH = 22;
		for (NSDictionary *c in counts) {
			NSColor *sc = stateColor(c[@"state"]);
			NSTextField *t = textLabel(c[@"label"], 11.5, NSFontWeightMedium, sc);
			CGFloat w = 8 + 7 + 6 + t.frame.size.width + 10;
			if (x > pad && x + w > menuWidth - pad) {
				x = pad;
				y += chipH + 6;
			}
			NSView *chip = filledView(NSMakeRect(x, y, w, chipH), [sc colorWithAlphaComponent:0.12], chipH / 2);
			chip.layer.borderWidth = 1;
			chip.layer.borderColor = [sc colorWithAlphaComponent:0.35].CGColor;
			NSImageView *dot = [NSImageView imageViewWithImage:dotImage(sc, 7)];
			dot.frame = NSMakeRect(9, (chipH - 7) / 2, 7, 7);
			[chip addSubview:dot];
			t.frame = NSMakeRect(9 + 7 + 6, (chipH - t.frame.size.height) / 2, t.frame.size.width + 2, t.frame.size.height);
			[chip addSubview:t];
			[root addSubview:chip];
			x += w + 6;
		}
		y += chipH + 12;
	} else if (empty && notice.length > 0) {
		NSTextField *t = textLabel(notice, 12.5, NSFontWeightRegular, colFaint());
		t.alignment = NSTextAlignmentCenter;
		t.lineBreakMode = NSLineBreakByWordWrapping;
		t.maximumNumberOfLines = 3;
		t.preferredMaxLayoutWidth = inner;
		CGFloat h = [t sizeThatFits:NSMakeSize(inner, 1000)].height;
		t.frame = NSMakeRect(pad, y + 10, inner, h);
		[root addSubview:t];
		y += 10 + h + 22;
	}

	// A bar that shows the share of each state.
	if (counts.count > 0) {
		int total = 0;
		for (NSDictionary *c in counts) total += [c[@"n"] intValue];
		NSView *track = filledView(NSMakeRect(pad, y, inner, 6), colCard(), 3);
		track.layer.masksToBounds = YES;
		CGFloat bx = 0;
		for (NSDictionary *c in counts) {
			CGFloat w = round(inner * [c[@"n"] intValue] / total);
			if (c == counts.lastObject) w = inner - bx;
			[track addSubview:filledView(NSMakeRect(bx, 0, w, 6), stateColor(c[@"state"]), 0)];
			bx += w;
		}
		[root addSubview:track];
		y += 6 + 12;
	}

	// An error after a click on a card.
	if (notice.length > 0 && !empty) {
		NSTextField *t = textLabel(notice, 12, NSFontWeightRegular, rgb(0xFFC857));
		t.lineBreakMode = NSLineBreakByWordWrapping;
		t.maximumNumberOfLines = 3;
		t.preferredMaxLayoutWidth = inner;
		NSSize sz = [t sizeThatFits:NSMakeSize(inner, 1000)];
		t.frame = NSMakeRect(pad, y, inner, sz.height);
		[root addSubview:t];
		y += sz.height + 10;
	}

	// The session cards. More than maxVisibleCards cards scroll.
	if (rows.count > 0) {
		CGFloat full = rows.count * (cardHeight + cardGap) - cardGap;
		CGFloat visible = MIN(full, maxVisibleCards * (cardHeight + cardGap) - cardGap + cardHeight / 2);
		CWFlippedView *list = [[CWFlippedView alloc] initWithFrame:NSMakeRect(0, 0, inner, full)];
		CGFloat cy = 0;
		for (NSDictionary *r in rows) {
			[list addSubview:[self card:r frame:NSMakeRect(0, cy, inner, cardHeight)]];
			cy += cardHeight + cardGap;
		}
		NSScrollView *sv = [[NSScrollView alloc] initWithFrame:NSMakeRect(pad, y, inner, visible)];
		sv.drawsBackground = NO;
		sv.hasVerticalScroller = full > visible;
		sv.scrollerStyle = NSScrollerStyleOverlay;
		sv.autohidesScrollers = YES;
		sv.documentView = list;
		if (self.scroll && scrollAt.y > 0) {
			[sv.contentView scrollToPoint:NSMakePoint(0, MIN(scrollAt.y, full - visible))];
			[sv reflectScrolledClipView:sv.contentView];
		}
		self.scroll = sv;
		[root addSubview:sv];
		y += visible + 8;
	} else {
		self.scroll = nil;
	}

	NSNumber *more = d[@"more"];
	if (more.intValue > 0) {
		NSString *s = [NSString stringWithFormat:@"%@ more. Open the dashboard to see all.", more];
		NSTextField *t = textLabel(s, 11, NSFontWeightRegular, colFaint());
		t.frame = NSMakeRect(pad, y, inner, t.frame.size.height);
		[root addSubview:t];
		y += t.frame.size.height + 8;
	}

	// The footer.
	[root addSubview:filledView(NSMakeRect(0, y, menuWidth, 1), colLine(), 0)];
	y += 1;
	CGFloat fh = 38;
	NSButton *dash = [self textButton:@"Open the dashboard  →" color:colMint() action:@selector(openDashboard:)];
	dash.frame = NSMakeRect(pad - 4, y + (fh - dash.frame.size.height) / 2, dash.frame.size.width, dash.frame.size.height);
	[root addSubview:dash];
	NSButton *quit = [self textButton:@"Quit" color:colFaint() action:@selector(quit:)];
	quit.toolTip = @"Stop the menu bar until the next login";
	quit.frame = NSMakeRect(menuWidth - pad + 4 - quit.frame.size.width, y + (fh - quit.frame.size.height) / 2,
		quit.frame.size.width, quit.frame.size.height);
	[root addSubview:quit];
	y += fh;

	// Keep the top of the panel below the status item.
	NSRect br = [self.item.button.window convertRectToScreen:[self.item.button convertRect:self.item.button.bounds toView:nil]];
	NSScreen *screen = self.item.button.window.screen ?: NSScreen.mainScreen;
	NSRect vf = screen.visibleFrame;
	CGFloat x = NSMidX(br) - menuWidth / 2;
	x = MAX(NSMinX(vf) + 8, MIN(x, NSMaxX(vf) - menuWidth - 8));
	y = MIN(y, vf.size.height - 16);
	[self.panel setFrame:NSMakeRect(x, NSMinY(br) - 6 - y, menuWidth, y) display:YES];
	[self.panel invalidateShadow];

	// A card under the mouse pointer keeps its hover colour after the
	// refresh.
	NSPoint mouse = [self.panel mouseLocationOutsideOfEventStream];
	for (CWCard *c in self.scroll.documentView.subviews) {
		c.hover = NSPointInRect([c convertPoint:mouse fromView:nil], c.bounds);
	}
}

- (CWCard *)card:(NSDictionary *)r frame:(NSRect)f {
	NSString *st = r[@"state"];
	NSColor *sc = stateColor(st);
	BOOL attention = [st isEqual:@"needs_permission"] || [st isEqual:@"error"];

	CWCard *c = [[CWCard alloc] initWithFrame:f];
	c.sessionID = r[@"id"];
	c.toolTip = r[@"tooltip"];
	c.wantsLayer = YES;
	c.layer.cornerRadius = 8;
	c.layer.masksToBounds = YES;
	// A card that needs the user has a tint of its state colour.
	c.fill = attention ? [colCard() blendedColorWithFraction:0.2 ofColor:sc] : colCard();
	c.layer.backgroundColor = c.fill.CGColor;
	if (attention) {
		c.layer.borderWidth = 1;
		c.layer.borderColor = [sc colorWithAlphaComponent:0.4].CGColor;
	}
	c.accessibilityRole = NSAccessibilityButtonRole;
	c.accessibilityLabel = [NSString stringWithFormat:@"%@ %@, %@", r[@"project"], r[@"branch"] ?: @"", r[@"activity"]];

	[c addSubview:filledView(NSMakeRect(0, 0, 4, f.size.height), sc, 0)];

	CGFloat x = 14, right = f.size.width - 12;
	NSTextField *age = textLabel(r[@"age"], 11, NSFontWeightRegular, colFaint());
	age.font = [NSFont monospacedDigitSystemFontOfSize:11 weight:NSFontWeightRegular];
	[age sizeToFit];
	age.frame = NSMakeRect(right - age.frame.size.width, 9, age.frame.size.width, age.frame.size.height);
	[c addSubview:age];
	right = NSMinX(age.frame) - 8;

	NSTextField *proj = textLabel(r[@"project"], 13, NSFontWeightSemibold, colText());
	CGFloat pw = MIN(proj.frame.size.width, (right - x) * 0.6);
	proj.frame = NSMakeRect(x, 7, pw, proj.frame.size.height);
	[c addSubview:proj];
	NSString *branch = r[@"branch"];
	if (branch.length > 0) {
		NSTextField *br = textLabel([@"⎇ " stringByAppendingString:branch], 11.5, NSFontWeightRegular, colBranch());
		CGFloat bx = NSMaxX(proj.frame) + 8;
		br.frame = NSMakeRect(bx, 8.5, MAX(0, MIN(br.frame.size.width, right - bx)), br.frame.size.height);
		[c addSubview:br];
	}

	NSString *line = [NSString stringWithFormat:@"%@  %@", r[@"icon"], r[@"activity"]];
	NSTextField *act = textLabel(line, 12, NSFontWeightRegular, attention ? [colText() blendedColorWithFraction:0.3 ofColor:sc] : colFaint());
	act.frame = NSMakeRect(x, 28, f.size.width - x - 12, act.frame.size.height);
	[c addSubview:act];
	return c;
}

- (NSButton *)textButton:(NSString *)title color:(NSColor *)color action:(SEL)action {
	NSButton *b = [NSButton buttonWithTitle:title target:self action:action];
	b.bordered = NO;
	b.attributedTitle = [[NSAttributedString alloc] initWithString:title attributes:@{
		NSForegroundColorAttributeName: color,
		NSFontAttributeName: [NSFont systemFontOfSize:12.5 weight:NSFontWeightMedium],
	}];
	[b sizeToFit];
	return b;
}

- (void)openDashboard:(id)sender {
	[self close];
	cwatchMenuEvent(eventDashboard, "");
}

- (void)quit:(id)sender {
	[NSApp terminate:nil];
}

@end

static CWMenu *theMenu;

// cwatchRunApp starts the menu bar item and runs the event loop. It does not
// return. It must run on the main thread.
static void cwatchRunApp(void) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		theMenu = [CWMenu new];
		[theMenu setup];
	}
	[NSApp run];
}

// cwatchSetMenu gives new data to the menu, as JSON.
static void cwatchSetMenu(char *json) {
	NSData *data = [NSData dataWithBytes:json length:strlen(json)];
	dispatch_async(dispatch_get_main_queue(), ^{
		NSDictionary *d = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
		if ([d isKindOfClass:NSDictionary.class]) {
			[theMenu update:d];
		}
	});
}

// cwatchCloseMenu closes the panel.
static void cwatchCloseMenu(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[theMenu close];
	});
}

*/
import "C"

import (
	"encoding/json"
	"unsafe"
)

// runApp shows the menu bar item and runs the macOS event loop. It does not
// return.
func runApp() { C.cwatchRunApp() }

// setView gives a new view to the menu bar item and the panel.
func setView(v View) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s := C.CString(string(b))
	defer C.free(unsafe.Pointer(s))
	C.cwatchSetMenu(s)
}

// closePanel closes the session panel.
func closePanel() { C.cwatchCloseMenu() }
