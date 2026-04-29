import AVFoundation
import Foundation

final class PlayerDelegate: NSObject, AVAudioPlayerDelegate {
    var onFinish: (() -> Void)?

    func audioPlayerDidFinishPlaying(_ player: AVAudioPlayer, successfully flag: Bool) {
        onFinish?()
    }
}

guard CommandLine.arguments.count >= 3 else {
    fputs("usage: mac-player-helper <file> <volume>\n", stderr)
    exit(2)
}

let filePath = CommandLine.arguments[1]
let initialVolume = Float(CommandLine.arguments[2]) ?? 1.0

do {
    let player = try AVAudioPlayer(contentsOf: URL(fileURLWithPath: filePath))
    player.volume = max(0, min(1, initialVolume))
    let delegate = PlayerDelegate()
    delegate.onFinish = {
        exit(EXIT_SUCCESS)
    }
    player.delegate = delegate
    player.prepareToPlay()
    player.play()

    DispatchQueue.global(qos: .userInitiated).async {
        while let line = readLine() {
            let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
            if trimmed.isEmpty {
                continue
            }
            let parts = trimmed.split(separator: " ", maxSplits: 1).map(String.init)
            DispatchQueue.main.async {
                switch parts[0] {
                case "pause":
                    player.pause()
                case "resume":
                    player.play()
                case "stop":
                    player.stop()
                    exit(EXIT_SUCCESS)
                case "volume":
                    if parts.count == 2, let value = Float(parts[1]) {
                        player.volume = max(0, min(1, value))
                    }
                default:
                    break
                }
            }
        }
        DispatchQueue.main.async {
            exit(EXIT_SUCCESS)
        }
    }

    RunLoop.main.run()
} catch {
    fputs("mac-player-helper error: \(error)\n", stderr)
    exit(1)
}
