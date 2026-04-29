import AVFoundation
import Foundation

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

func stopCurrent(report: Bool) {
    if report, currentToken != 0 {
        print("stopped\t\(currentToken)")
        fflush(stdout)
    }
    currentPlayer?.stop()
    currentPlayer = nil
    currentToken = 0
}

func playTrack(token: Int, volume: Float, path: String) {
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
        if player.duration > startupTrimSeconds + 0.05 {
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
    while let raw = readLine() {
        let line = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if line.isEmpty {
            continue
        }
        let parts = line.split(separator: "\t", omittingEmptySubsequences: false).map(String.init)
        DispatchQueue.main.async {
            switch parts.first {
            case "play":
                if parts.count >= 4, let token = Int(parts[1]), let volume = Float(parts[2]) {
                    playTrack(token: token, volume: volume, path: parts[3])
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
