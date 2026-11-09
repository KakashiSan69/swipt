import Perception
import SwiftUI

@available(iOS 17.4, *)
struct SwipletsView: View {
    @State private var isAddingSwiplet = false
    @State private var isShowingUpdates = false
    @State private var swipletToDelete: String?
    @State private var swipletInfo: String?
    @State private var outdatedSwiplet: String?
    @State private var alert: AlertState?

    private let swiplets = SwipletStore.shared

    var body: some View {
        WithPerceptionTracking {
            Group {
                if swiplets.ids.isEmpty {
                    ContentUnavailableViewCompat {
                        Label("No Swiplets", systemImage: "wand.and.stars")
                    } description: {
                        Text("Tap \(Image(systemName: "plus")) to add a swiplet.")
                    }
                } else {
                    Form {
                        swipletsSection
                    }
                }
            }
            .navigationTitle("Swiplets")
            .navigationDestination(item: $swipletInfo) { id in
                SwipletInfoView(id: id)
            }
            .toolbar {
                if !swiplets.updates.isEmpty {
                    ToolbarItem(placement: .primaryAction) {
                        Button {
                            isShowingUpdates = true
                        } label: {
                            Label("Updates", systemImage: "arrow.down.circle")
                        }
                    }
                }
                ToolbarItem(placement: .primaryAction) {
                    Button {
                        isAddingSwiplet = true
                    } label: {
                        Label("Add Swiplet", systemImage: "plus")
                    }
                }
            }
            .sheet(isPresented: $isAddingSwiplet) {
                NavigationStackCompat {
                    NewSwipletView()
                }
            }
            .sheet(isPresented: $isShowingUpdates) {
                NavigationStackCompat {
                    SwipletUpdatesView()
                }
            }
            .onAppear {
                SwipletHost.shared.prepare(swiplets.interfaces)
            }
            .onChange(of: swiplets.interfaces) {
                SwipletHost.shared.prepare(swiplets.interfaces)
            }
            .task { await swiplets.checkForUpdates(of: swiplets.ids) }
            .alert(
                "Delete “\(swipletToDelete.map { swiplets.titles[$0] ?? $0 } ?? "")”?",
                isPresented: Binding(get: { swipletToDelete != nil }, set: { if !$0 { swipletToDelete = nil } })
            ) {
                Button("Cancel", role: .cancel) {}
                Button("Delete", role: .destructive) {
                    if let swipletToDelete {
                        Task {
                            do {
                                try await swiplets.remove(swipletToDelete)
                            } catch {
                                alert = AlertState(String(localized: "Can’t Delete the Swiplet"), error: error)
                            }
                        }
                    }
                    swipletToDelete = nil
                }
            }
            .alert(
                "Swiplet Outdated",
                isPresented: Binding(get: { outdatedSwiplet != nil }, set: { if !$0 { outdatedSwiplet = nil } }),
                presenting: outdatedSwiplet
            ) { _ in
                Button("OK") {}
            } message: { name in
                Text("“\(swiplets.titles[name] ?? name)” needs to be updated to work with this version of Swipt.")
            }
            .alert($alert)
        }
    }

    private var swipletsSection: some View {
        Section {
            ForEach(swiplets.ids, id: \.self, content: swipletRow)
                .onMove { source, destination in
                    var ids = swiplets.ids
                    ids.move(fromOffsets: source, toOffset: destination)
                    swiplets.reorder(ids)
                }
        }
    }

    private func swipletRow(_ name: String) -> some View {
        let service = swiplets.withServices.contains(name) ? swiplets.service(of: name) : nil
        let interface = swiplets.interfaces[name]
        return Group {
            if swiplets.outdated.contains(name) {
                Button {
                    outdatedSwiplet = name
                } label: {
                    swipletLabel(name)
                }
                .tint(.primary)
            } else if let interface {
                Button {
                    SwipletPresentation.open(name: name, entry: interface)
                } label: {
                    swipletLabel(name)
                }
                .tint(.primary)
            } else if let service {
                Toggle(isOn: Binding(get: { service.enabled }, set: { setService(name, enabled: $0) })) {
                    swipletLabel(name)
                }
            } else {
                swipletLabel(name)
            }
        }
        .swipeActions(edge: .trailing) {
            Button {
                swipletToDelete = name
            } label: {
                Label("Delete", systemImage: "trash")
            }
            .tint(.red)
        }
        .contextMenu {
            Button {
                swipletInfo = name
            } label: {
                Label("Info", systemImage: "info.circle")
            }
            Button {
                share(name)
            } label: {
                Label("Share", systemImage: "square.and.arrow.up")
            }
            if swiplets.updates[name] == nil, swiplets.updatable.contains(name) {
                Button {
                    checkForUpdate(name)
                } label: {
                    Label("Check for Updates…", systemImage: "arrow.clockwise")
                }
            }
            Button(role: .destructive) {
                swipletToDelete = name
            } label: {
                Label("Delete", systemImage: "trash")
            }
        }
    }

    private func swipletLabel(_ name: String) -> some View {
        HStack(spacing: 12) {
            SwipletIconImage(icon: swiplets.icons[name] ?? .default, size: 40)
            Text(verbatim: swiplets.titles[name] ?? name)
                .lineLimit(1)
        }
    }

    private func setService(_ name: String, enabled: Bool) {
        Task {
            do {
                try await swiplets.setServiceEnabled(name, enabled)
            } catch {
                let title = swiplets.titles[name] ?? name
                alert = AlertState(enabled ? String(localized: "Can’t Turn On “\(title)”") : String(localized: "Can’t Turn Off “\(title)”"), error: error)
            }
        }
    }

    private func share(_ name: String) {
        Task {
            do {
                let file = try await swiplets.export(name)
                guard let top = topViewController() else { return }
                let controller = UIActivityViewController(activityItems: [file], applicationActivities: nil)
                controller.completionWithItemsHandler = { _, _, _, _ in
                    try? FileManager.default.removeItem(at: file.deletingLastPathComponent())
                }
                if let popover = controller.popoverPresentationController {
                    popover.sourceView = top.view
                    popover.sourceRect = CGRect(origin: CGPoint(x: top.view.bounds.midX, y: top.view.bounds.midY), size: .zero)
                    popover.permittedArrowDirections = []
                }
                top.present(controller, animated: true)
            } catch {
                alert = AlertState(String(localized: "Can’t Share the Swiplet"), error: error)
            }
        }
    }

    private func checkForUpdate(_ name: String) {
        Task {
            do {
                let found = try await swiplets.checkForUpdate(of: name)
                if !found {
                    alert = AlertState(title: String(localized: "You’re Up to Date"))
                }
            } catch {
                alert = AlertState(String(localized: "Couldn’t Check for Updates"), error: error)
            }
        }
    }
}

@available(iOS 17.4, *)
struct SwipletUpdatesView: View {
    @Environment(\.dismiss) private var dismiss
    @State private var alert: AlertState?

    private let swiplets = SwipletStore.shared

    var body: some View {
        WithPerceptionTracking {
            let pending = swiplets.ids.compactMap { swiplets.updates[$0] }
            Form {
                Section {
                    Button("Update All") { apply(pending.map(\.id)) }
                        .disabled(pending.contains { swiplets.updating.contains($0.id) })
                }
                Section("Updatable Swiplets") {
                    ForEach(pending, id: \.id, content: row)
                }
            }
            .navigationTitle("Updates")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    CloseButton { dismiss() }
                }
            }
            .onChange(of: pending.isEmpty) {
                if pending.isEmpty {
                    dismiss()
                }
            }
            .alert($alert)
        }
    }

    private func row(_ update: SwipletUpdate) -> some View {
        HStack(spacing: 12) {
            SwipletIconImage(icon: swiplets.icons[update.id] ?? .default, size: 40)
            VStack(alignment: .leading, spacing: 2) {
                Text(verbatim: swiplets.titles[update.id] ?? update.id)
                    .lineLimit(1)
                if !update.version.isEmpty {
                    SwipletVersionChange(installed: swiplets.versions[update.id] ?? "", available: update.version)
                }
            }
            Spacer()
            if swiplets.updating.contains(update.id) {
                SwipletDownloadIndicator(progress: swiplets.downloadProgress[update.id])
            } else {
                Button("Update") { apply([update.id]) }
                    .buttonStyle(.bordered)
                    .buttonBorderShape(.capsule)
            }
        }
    }

    private func apply(_ ids: [String]) {
        Task {
            do {
                try await swiplets.applyUpdates(ids)
            } catch {
                alert = AlertState(String(localized: "Can’t Update the Swiplet"), error: error)
            }
        }
    }
}

struct SwipletVersionChange: View {
    let installed: String
    let available: String

    var body: some View {
        Group {
            if installed.isEmpty || installed == available {
                Text(verbatim: available)
            } else {
                HStack(spacing: 4) {
                    Text(verbatim: installed)
                    Image(systemName: "arrow.forward")
                        .imageScale(.small)
                    Text(verbatim: available)
                }
            }
        }
        .font(.footnote)
        .foregroundStyle(.secondary)
    }
}

struct SwipletDownloadIndicator: View {
    let progress: SwipletDownloadProgress?

    var body: some View {
        HStack(spacing: 8) {
            if let progress {
                let received = progress.received.formatted(.byteCount(style: .file))
                Text(verbatim: progress.total > 0 ? "\(received) / \(progress.total.formatted(.byteCount(style: .file)))" : received)
                    .font(.footnote)
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
            }
            ProgressView()
        }
    }
}
