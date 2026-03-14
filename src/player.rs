use std::io::{self, BufReader, Read, Seek, SeekFrom};

use anyhow::{Context, Result};
use crossbeam_channel::Receiver;
use rodio::{Decoder, OutputStream, Sink};

pub struct StreamingPlayer {
    _stream: OutputStream,
    sink: Sink,
}

impl StreamingPlayer {
    pub fn new() -> Result<Self> {
        let (stream, stream_handle) =
            OutputStream::try_default().context("failed to open audio output stream")?;
        let sink = Sink::try_new(&stream_handle).context("failed to create audio sink")?;

        Ok(Self {
            _stream: stream,
            sink,
        })
    }

    pub fn play_stream(&self, receiver: Receiver<Vec<u8>>) -> Result<()> {
        let stream_buffer = StreamBuffer::new(receiver);
        let decoder = Decoder::new(BufReader::new(stream_buffer))
            .context("failed to create streaming audio decoder")?;

        self.sink.append(decoder);
        self.sink.play();
        Ok(())
    }

    pub fn is_empty(&self) -> bool {
        self.sink.empty()
    }

    pub fn pause(&self) {
        self.sink.pause();
    }

    pub fn resume(&self) {
        self.sink.play();
    }

    pub fn stop(&self) {
        self.sink.stop();
    }

    pub fn set_volume(&self, volume: f32) {
        self.sink.set_volume(volume.clamp(0.0, 1.0));
    }
}

pub struct StreamBuffer {
    receiver: Receiver<Vec<u8>>,
    /// All bytes received so far (grows indefinitely but lets us seek backward).
    buffer: Vec<u8>,
    /// Current logical read position within `buffer`.
    position: usize,
    closed: bool,
}

impl StreamBuffer {
    pub fn new(receiver: Receiver<Vec<u8>>) -> Self {
        Self {
            receiver,
            buffer: Vec::new(),
            position: 0,
            closed: false,
        }
    }

    /// Pull more chunks from the receiver until `buffer` has at least `need` bytes,
    /// or the sender is closed.
    fn fill_to(&mut self, need: usize) -> io::Result<()> {
        while self.buffer.len() < need && !self.closed {
            match self.receiver.recv() {
                Ok(chunk) if !chunk.is_empty() => self.buffer.extend_from_slice(&chunk),
                Ok(_) => continue,
                Err(_) => self.closed = true,
            }
        }
        Ok(())
    }
}

impl Read for StreamBuffer {
    fn read(&mut self, out: &mut [u8]) -> io::Result<usize> {
        if out.is_empty() {
            return Ok(0);
        }
        // Ensure at least one byte beyond current position is available.
        let want = self.position + out.len();
        self.fill_to(want)?;

        let available = self.buffer.len().saturating_sub(self.position);
        if available == 0 {
            return Ok(0);
        }
        let n = out.len().min(available);
        out[..n].copy_from_slice(&self.buffer[self.position..self.position + n]);
        self.position += n;
        Ok(n)
    }
}

impl Seek for StreamBuffer {
    fn seek(&mut self, pos: SeekFrom) -> io::Result<u64> {
        match pos {
            SeekFrom::Start(n) => {
                let n = n as usize;
                self.fill_to(n)?;
                self.position = n.min(self.buffer.len());
                Ok(self.position as u64)
            }
            SeekFrom::Current(delta) => {
                let new_pos = (self.position as i64 + delta).max(0) as usize;
                self.fill_to(new_pos)?;
                self.position = new_pos.min(self.buffer.len());
                Ok(self.position as u64)
            }
            SeekFrom::End(delta) => {
                // We don't know the end until the stream is fully received.
                // Drain the receiver so we know the true length.
                while !self.closed {
                    match self.receiver.recv() {
                        Ok(chunk) if !chunk.is_empty() => self.buffer.extend_from_slice(&chunk),
                        Ok(_) => continue,
                        Err(_) => self.closed = true,
                    }
                }
                let new_pos = (self.buffer.len() as i64 + delta).max(0) as usize;
                self.position = new_pos.min(self.buffer.len());
                Ok(self.position as u64)
            }
        }
    }
}
