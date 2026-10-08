#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>

// Observe the actual NSWindow ordering boundary without putting test getters
// in the GUI host. This library is loaded only by the AppKit regression child.
static void (*orderFront)(id, SEL, id);

static void observeOrder(NSWindow *window, SEL selector, id sender) {
    if ([window.title isEqualToString:@"magpie"]) {
        printf("space order: visible=%d minimised=%d move=%d primary=%d\n",
               window.visible, window.miniaturized,
               !!(window.collectionBehavior & NSWindowCollectionBehaviorMoveToActiveSpace),
               !!(window.collectionBehavior & NSWindowCollectionBehaviorFullScreenPrimary));
        fflush(stdout);
    }
    orderFront(window, selector, sender);
}

__attribute__((constructor)) static void observeMainWindow(void) {
    Method method = class_getInstanceMethod([NSWindow class], @selector(makeKeyAndOrderFront:));
    orderFront = (void (*)(id, SEL, id))method_getImplementation(method);
    method_setImplementation(method, (IMP)observeOrder);
}
