//go:build darwin && cgo

package menubar

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>

#import <Cocoa/Cocoa.h>

extern void cwatchPanelAction(char *id, int goTo);

// The kinds are the same as AlertKind in Go.
enum { kindPermission = 0, kindError = 1, kindIdle = 2 };

static const CGFloat panelWidth = 360;
static const CGFloat panelHeight = 92;
static const CGFloat panelMargin = 12;
static const NSTimeInterval idleTime = 8;

// roundedMask makes the corners of the card round. A mask on the effect
// view also gives the window a shadow with round corners.
static NSImage *roundedMask(CGFloat r) {
	NSImage *img = [NSImage imageWithSize:NSMakeSize(2 * r + 1, 2 * r + 1) flipped:NO drawingHandler:^BOOL(NSRect rect) {
		[NSColor.blackColor setFill];
		[[NSBezierPath bezierPathWithRoundedRect:rect xRadius:r yRadius:r] fill];
		return YES;
	}];
	img.capInsets = NSEdgeInsetsMake(r, r, r, r);
	img.resizingMode = NSImageResizingModeStretch;
	return img;
}

static NSTextField *label(CGFloat size, NSFontWeight weight, NSColor *color, NSRect frame) {
	NSTextField *l = [NSTextField labelWithString:@""];
	l.font = [NSFont systemFontOfSize:size weight:weight];
	l.textColor = color;
	l.lineBreakMode = NSLineBreakByTruncatingTail;
	l.frame = frame;
	return l;
}

@interface CWAlertPanel : NSObject
@property (strong) NSPanel *panel;
@property (strong) NSBox *accent;
@property (strong) NSTextField *titleLabel;
@property (strong) NSTextField *bodyLabel;
@property (copy) NSString *alertID;
@property (strong) NSTimer *timer;
@end

@implementation CWAlertPanel

- (void)build {
	NSRect frame = NSMakeRect(0, 0, panelWidth, panelHeight);
	// A non-activating panel does not take the keyboard focus from the pane
	// that the user types in.
	NSPanel *p = [[NSPanel alloc] initWithContentRect:frame
		styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
		backing:NSBackingStoreBuffered defer:YES];
	p.level = NSStatusWindowLevel;
	p.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces |
		NSWindowCollectionBehaviorFullScreenAuxiliary |
		NSWindowCollectionBehaviorStationary |
		NSWindowCollectionBehaviorIgnoresCycle;
	p.opaque = NO;
	p.backgroundColor = NSColor.clearColor;
	p.hasShadow = YES;
	p.hidesOnDeactivate = NO;
	p.releasedWhenClosed = NO;
	p.floatingPanel = YES;
	p.becomesKeyOnlyIfNeeded = YES;

	NSVisualEffectView *bg = [[NSVisualEffectView alloc] initWithFrame:frame];
	bg.material = NSVisualEffectMaterialPopover;
	bg.blendingMode = NSVisualEffectBlendingModeBehindWindow;
	bg.state = NSVisualEffectStateActive;
	bg.maskImage = roundedMask(12);
	p.contentView = bg;

	NSBox *accent = [[NSBox alloc] initWithFrame:NSMakeRect(0, 0, 5, panelHeight)];
	accent.boxType = NSBoxCustom;
	accent.borderWidth = 0;
	[bg addSubview:accent];

	CGFloat x = 20, w = panelWidth - x - 16;
	self.titleLabel = label(13, NSFontWeightSemibold, NSColor.labelColor, NSMakeRect(x, 60, w, 18));
	self.bodyLabel = label(12, NSFontWeightRegular, NSColor.secondaryLabelColor, NSMakeRect(x, 41, w, 16));
	[bg addSubview:self.titleLabel];
	[bg addSubview:self.bodyLabel];

	NSButton *go = [NSButton buttonWithTitle:@"Go to pane" target:self action:@selector(goToPane:)];
	go.bezelColor = NSColor.controlAccentColor;
	[go sizeToFit];
	go.frame = NSMakeRect(panelWidth - 14 - go.frame.size.width, 10, go.frame.size.width, go.frame.size.height);
	NSButton *dismiss = [NSButton buttonWithTitle:@"Dismiss" target:self action:@selector(dismiss:)];
	[dismiss sizeToFit];
	dismiss.frame = NSMakeRect(NSMinX(go.frame) - 8 - dismiss.frame.size.width, 10, dismiss.frame.size.width, dismiss.frame.size.height);
	[bg addSubview:go];
	[bg addSubview:dismiss];

	// A click on the card, not on a button, also goes to the pane.
	NSClickGestureRecognizer *click = [[NSClickGestureRecognizer alloc] initWithTarget:self action:@selector(goToPane:)];
	[bg addGestureRecognizer:click];

	self.panel = p;
	self.accent = accent;
}

- (void)showID:(NSString *)alertID title:(NSString *)title body:(NSString *)body kind:(int)kind sticky:(BOOL)sticky {
	if (!self.panel) {
		[self build];
	}
	self.alertID = alertID;
	self.titleLabel.stringValue = title;
	self.bodyLabel.stringValue = body;
	switch (kind) {
	case kindPermission:
		self.accent.fillColor = NSColor.systemYellowColor;
		break;
	case kindError:
		self.accent.fillColor = NSColor.systemRedColor;
		break;
	default:
		self.accent.fillColor = NSColor.systemGrayColor;
	}

	[self.timer invalidate];
	self.timer = nil;
	if (!sticky) {
		self.timer = [NSTimer scheduledTimerWithTimeInterval:idleTime target:self selector:@selector(timeout:) userInfo:nil repeats:NO];
	}

	// Show the card in the top-right corner of the screen that the user
	// works on, below the menu bar.
	NSScreen *screen = NSScreen.mainScreen ?: NSScreen.screens.firstObject;
	NSRect vf = screen.visibleFrame;
	[self.panel setFrameOrigin:NSMakePoint(NSMaxX(vf) - panelWidth - panelMargin, NSMaxY(vf) - panelHeight - panelMargin)];
	if (!self.panel.visible) {
		self.panel.alphaValue = 0;
		[self.panel orderFrontRegardless];
	}
	[NSAnimationContext runAnimationGroup:^(NSAnimationContext *ctx) {
		ctx.duration = 0.2;
		self.panel.animator.alphaValue = 1;
	}];

	NSAccessibilityPostNotificationWithUserInfo(NSApp, NSAccessibilityAnnouncementRequestedNotification, @{
		NSAccessibilityAnnouncementKey: [NSString stringWithFormat:@"%@. %@", title, body],
		NSAccessibilityPriorityKey: @(NSAccessibilityPriorityHigh),
	});
}

- (void)hide {
	[self.timer invalidate];
	self.timer = nil;
	self.alertID = nil;
	if (!self.panel.visible) {
		return;
	}
	NSPanel *p = self.panel;
	[NSAnimationContext runAnimationGroup:^(NSAnimationContext *ctx) {
		ctx.duration = 0.2;
		p.animator.alphaValue = 0;
	} completionHandler:^{
		// A new alert can come during the animation. Keep it on the screen.
		if (self.alertID == nil) {
			[p orderOut:nil];
		}
	}];
}

- (void)timeout:(NSTimer *)t {
	[self hide];
}

- (void)act:(int)goTo {
	NSString *alertID = self.alertID;
	[self hide];
	if (alertID != nil) {
		cwatchPanelAction((char *)alertID.UTF8String, goTo);
	}
}

- (void)goToPane:(id)sender {
	[self act:1];
}

- (void)dismiss:(id)sender {
	[self act:0];
}

@end

static CWAlertPanel *alertPanel;

static void cwatchShowPanel(char *alertID, char *title, char *body, int kind, int sticky) {
	NSString *i = [NSString stringWithUTF8String:alertID];
	NSString *t = [NSString stringWithUTF8String:title];
	NSString *b = [NSString stringWithUTF8String:body];
	dispatch_async(dispatch_get_main_queue(), ^{
		if (alertPanel == nil) {
			alertPanel = [CWAlertPanel new];
		}
		[alertPanel showID:i title:t body:b kind:kind sticky:sticky];
	});
}

static void cwatchHidePanel(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[alertPanel hide];
	});
}
*/
import "C"

import "unsafe"

// showPanel shows the alert card. Only one card shows at a time. A new
// alert replaces the card.
func showPanel(a Alert) {
	id, title, body := C.CString(a.ID), C.CString(a.Title), C.CString(a.Body)
	defer C.free(unsafe.Pointer(id))
	defer C.free(unsafe.Pointer(title))
	defer C.free(unsafe.Pointer(body))
	sticky := 0
	if a.Sticky() {
		sticky = 1
	}
	C.cwatchShowPanel(id, title, body, C.int(a.Kind), C.int(sticky))
}

// hidePanel removes the alert card from the screen.
func hidePanel() { C.cwatchHidePanel() }
