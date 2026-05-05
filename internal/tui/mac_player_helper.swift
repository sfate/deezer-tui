import AVFoundation
import Foundation
import AppKit
import MediaPlayer

final class PlayerDelegate: NSObject, AVAudioPlayerDelegate {
    var token: Int = 0

    func audioPlayerDidFinishPlaying(_ player: AVAudioPlayer, successfully flag: Bool) {
        print("finished\t\(token)")
        fflush(stdout)
    }
}

let warmupDurationSeconds: TimeInterval = 0.05
let startupTrimSeconds: TimeInterval = 0.035
let startupFadeSeconds: TimeInterval = 0.08
let resumeFadeSeconds: TimeInterval = 0.03

func makeSilentWAV(duration: TimeInterval, sampleRate: Int = 44100) -> Data {
    let channels = 1
    let bitsPerSample = 16
    let bytesPerSample = bitsPerSample / 8
    let frameCount = max(1, Int(duration * Double(sampleRate)))
    let dataSize = frameCount * channels * bytesPerSample
    let byteRate = sampleRate * channels * bytesPerSample
    let blockAlign = channels * bytesPerSample

    var data = Data()
    data.append("RIFF".data(using: .ascii)!)
    data.append(UInt32(36 + dataSize).littleEndianData)
    data.append("WAVE".data(using: .ascii)!)
    data.append("fmt ".data(using: .ascii)!)
    data.append(UInt32(16).littleEndianData)
    data.append(UInt16(1).littleEndianData)
    data.append(UInt16(channels).littleEndianData)
    data.append(UInt32(sampleRate).littleEndianData)
    data.append(UInt32(byteRate).littleEndianData)
    data.append(UInt16(blockAlign).littleEndianData)
    data.append(UInt16(bitsPerSample).littleEndianData)
    data.append("data".data(using: .ascii)!)
    data.append(UInt32(dataSize).littleEndianData)
    data.append(Data(count: dataSize))
    return data
}

func warmUpOutput() {
    let semaphore = DispatchSemaphore(value: 0)
    do {
        let silentData = makeSilentWAV(duration: warmupDurationSeconds)
        let warmupPlayer = try AVAudioPlayer(data: silentData)
        warmupPlayer.volume = 0
        warmupPlayer.prepareToPlay()
        warmupPlayer.play()
        DispatchQueue.global().asyncAfter(deadline: .now() + warmupDurationSeconds + 0.02) {
            warmupPlayer.stop()
            semaphore.signal()
        }
        _ = semaphore.wait(timeout: .now() + warmupDurationSeconds + 0.2)
    } catch {
        return
    }
}

extension FixedWidthInteger {
    var littleEndianData: Data {
        var value = self.littleEndian
        return withUnsafeBytes(of: &value) { Data($0) }
    }
}

let delegate = PlayerDelegate()
var currentPlayer: AVAudioPlayer?
var currentToken: Int = 0
var currentVolume: Float = 1.0
var currentTrackID: String = ""
var currentDurationMS: Int = 0
var currentPositionMS: Int = 0
var remoteCommandsRegistered = false

func emitRemote(_ command: String) {
    print("remote\t\(command)")
    fflush(stdout)
}

func registerRemoteCommands() {
    if remoteCommandsRegistered {
        return
    }
    remoteCommandsRegistered = true

    let center = MPRemoteCommandCenter.shared()
    center.playCommand.addTarget { _ in
        emitRemote("play")
        return .success
    }
    center.pauseCommand.addTarget { _ in
        emitRemote("pause")
        return .success
    }
    center.togglePlayPauseCommand.addTarget { _ in
        emitRemote("toggle")
        return .success
    }
    center.nextTrackCommand.addTarget { _ in
        emitRemote("next")
        return .success
    }
    center.previousTrackCommand.addTarget { _ in
        emitRemote("previous")
        return .success
    }
    center.changePlaybackPositionCommand.addTarget { event in
        guard let positionEvent = event as? MPChangePlaybackPositionCommandEvent else {
            return .commandFailed
        }
        let positionMS = max(0, Int(positionEvent.positionTime * 1000))
        emitRemote("setPosition\t\(positionMS)")
        return .success
    }
}

func updateNowPlaying(trackID: String, title: String, artist: String, durationMS: Int, positionMS: Int, artURL: String) {
    currentTrackID = trackID
    currentDurationMS = max(0, durationMS)
    currentPositionMS = max(0, positionMS)

    let info: [String: Any] = [
        MPMediaItemPropertyTitle: title,
        MPMediaItemPropertyArtist: artist,
        MPMediaItemPropertyPlaybackDuration: TimeInterval(currentDurationMS) / 1000.0,
        MPNowPlayingInfoPropertyElapsedPlaybackTime: TimeInterval(currentPositionMS) / 1000.0,
        MPNowPlayingInfoPropertyPlaybackRate: currentPlayer?.isPlaying == true ? 1.0 : 0.0,
    ]

    if let url = URL(string: artURL), !artURL.isEmpty {
        let artworkTrackID = trackID
        URLSession.shared.dataTask(with: url) { data, _, _ in
            if let data = data, let image = NSImage(data: data) {
                let artwork = MPMediaItemArtwork(boundsSize: image.size) { _ in image }
                DispatchQueue.main.async {
                    guard currentTrackID == artworkTrackID else {
                        return
                    }
                    var current = MPNowPlayingInfoCenter.default().nowPlayingInfo ?? info
                    current[MPMediaItemPropertyArtwork] = artwork
                    MPNowPlayingInfoCenter.default().nowPlayingInfo = current
                }
            }
        }.resume()
    }

    MPNowPlayingInfoCenter.default().nowPlayingInfo = info
}

func updatePlaybackState(_ state: String) {
    var info = MPNowPlayingInfoCenter.default().nowPlayingInfo ?? [:]
    switch state {
    case "playing":
        MPNowPlayingInfoCenter.default().playbackState = .playing
        info[MPNowPlayingInfoPropertyPlaybackRate] = 1.0
    case "paused":
        MPNowPlayingInfoCenter.default().playbackState = .paused
        info[MPNowPlayingInfoPropertyPlaybackRate] = 0.0
    default:
        MPNowPlayingInfoCenter.default().playbackState = .stopped
        info[MPNowPlayingInfoPropertyPlaybackRate] = 0.0
    }
    MPNowPlayingInfoCenter.default().nowPlayingInfo = info
}

func updatePosition(positionMS: Int, durationMS: Int) {
    currentPositionMS = max(0, positionMS)
    currentDurationMS = max(0, durationMS)
    var info = MPNowPlayingInfoCenter.default().nowPlayingInfo ?? [:]
    info[MPNowPlayingInfoPropertyElapsedPlaybackTime] = TimeInterval(currentPositionMS) / 1000.0
    info[MPMediaItemPropertyPlaybackDuration] = TimeInterval(currentDurationMS) / 1000.0
    MPNowPlayingInfoCenter.default().nowPlayingInfo = info
}

func updateCapabilities(canNext: Bool, canPrevious: Bool, canSeek: Bool) {
    let center = MPRemoteCommandCenter.shared()
    center.nextTrackCommand.isEnabled = canNext
    center.previousTrackCommand.isEnabled = canPrevious
    center.changePlaybackPositionCommand.isEnabled = canSeek
}

func stopCurrent(report: Bool) {
    if report, currentToken != 0 {
        print("stopped\t\(currentToken)")
        fflush(stdout)
    }
    currentPlayer?.stop()
    currentPlayer = nil
    currentToken = 0
}

func playTrack(token: Int, volume: Float, seekMS: Int, path: String) {
    stopCurrent(report: false)
    warmUpOutput()
    do {
        let player = try AVAudioPlayer(contentsOf: URL(fileURLWithPath: path))
        currentToken = token
        currentVolume = max(0, min(1, volume))
        player.volume = 0
        delegate.token = token
        player.delegate = delegate
        player.prepareToPlay()
        let requestedStart = max(0, TimeInterval(seekMS) / 1000.0)
        if requestedStart > 0 && requestedStart < player.duration {
            player.currentTime = requestedStart
        } else if player.duration > startupTrimSeconds + 0.05 {
            player.currentTime = startupTrimSeconds
        }
        currentPlayer = player
        player.play()
        player.setVolume(currentVolume, fadeDuration: startupFadeSeconds)
    } catch {
        print("error\t\(token)\t\(error)")
        fflush(stdout)
    }
}

DispatchQueue.global(qos: .userInitiated).async {
    DispatchQueue.main.async {
        registerRemoteCommands()
    }
    while let raw = readLine() {
        let line = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if line.isEmpty {
            continue
        }
        let parts = line.split(separator: "\t", omittingEmptySubsequences: false).map(String.init)
        DispatchQueue.main.async {
            switch parts.first {
            case "play":
                if parts.count >= 5, let token = Int(parts[1]), let volume = Float(parts[2]), let seekMS = Int(parts[3]) {
                    playTrack(token: token, volume: volume, seekMS: seekMS, path: parts[4])
                }
            case "pause":
                currentPlayer?.pause()
            case "resume":
                if let player = currentPlayer {
                    player.volume = 0
                    player.play()
                    player.setVolume(currentVolume, fadeDuration: resumeFadeSeconds)
                }
            case "stop":
                stopCurrent(report: true)
            case "volume":
                if parts.count >= 2, let value = Float(parts[1]), let player = currentPlayer {
                    currentVolume = max(0, min(1, value))
                    player.setVolume(currentVolume, fadeDuration: resumeFadeSeconds)
                }
            case "nowplaying":
                if parts.count >= 7, let durationMS = Int(parts[4]), let positionMS = Int(parts[5]) {
                    updateNowPlaying(trackID: parts[1], title: parts[2], artist: parts[3], durationMS: durationMS, positionMS: positionMS, artURL: parts[6])
                }
            case "state":
                if parts.count >= 2 {
                    updatePlaybackState(parts[1])
                }
            case "position":
                if parts.count >= 3, let positionMS = Int(parts[1]), let durationMS = Int(parts[2]) {
                    updatePosition(positionMS: positionMS, durationMS: durationMS)
                }
            case "capabilities":
                if parts.count >= 4 {
                    updateCapabilities(canNext: parts[1] == "1", canPrevious: parts[2] == "1", canSeek: parts[3] == "1")
                }
            case "quit":
                stopCurrent(report: false)
                exit(EXIT_SUCCESS)
            default:
                break
            }
        }
    }
    DispatchQueue.main.async {
        stopCurrent(report: false)
        exit(EXIT_SUCCESS)
    }
}

RunLoop.main.run()
