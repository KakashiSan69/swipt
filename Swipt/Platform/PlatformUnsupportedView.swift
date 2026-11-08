import SwiftUI
import UIKit

struct PlatformUnsupportedView: View {
    let title: LocalizedStringKey

    private var systemImage: String {
        guard #available(iOS 17.4, *) else { return "exclamationmark.triangle" }
        return UIDevice.isPad ? "ipad.badge.exclamationmark" : "iphone.badge.exclamationmark"
    }

    var body: some View {
        ContentUnavailableViewCompat {
            if ProcessInfo.processInfo.isiOSAppOnMac {
                Label("Not Available on Mac", systemImage: "desktopcomputer")
            } else if UIDevice.isPad {
                Label("Unsupported iPadOS Version", systemImage: systemImage)
            } else {
                Label("Unsupported iOS Version", systemImage: systemImage)
            }
        } description: {
            if ProcessInfo.processInfo.isiOSAppOnMac {
                Text("Use this app on an iPhone or iPad to automate that device.")
            } else if UIDevice.isPad {
                Text("Automating this iPad needs iPadOS 17.4 or later.")
            } else {
                Text("Automating this iPhone needs iOS 17.4 or later.")
            }
        }
        .navigationTitle(title)
    }
}
