use tokio::sync::mpsc::{self, Receiver, Sender};
use tokio_util::sync::{CancellationToken, PollSendError, PollSender};
use std::pin::Pin;
use std::task::{Poll, Context};

pub struct DuplexChannel<R: Send + 'static, S: Send + 'static> {
    pub sink: Receiver<R>,
    pub producer: Sender<S>,
}
impl <R: Send, S:Send> DuplexChannel<R, S> {
    pub fn split(self) -> (Receiver<R>, Sender<S>) {
        (self.sink, self.producer)
    }
    pub fn new(buffer: usize) -> (DuplexChannel<R, S>, DuplexChannel<S, R>) {
        let (tx1, recv1) = mpsc::channel::<S>(buffer);
        let (tx2, recv2) = mpsc::channel::<R>(buffer);

        (
            DuplexChannel{
                sink: recv2,
                producer: tx1, 
            },
            DuplexChannel{
                sink: recv1,
                producer: tx2,
            }
        )
    }
    
    pub fn close(&mut self) {
        self.sink.close();
    }
}

pub fn map_channel<A: Send + 'static, B: Send + 'static, F>(mut recv: Receiver<A>, send: Sender<B>, f: F, cancellation_token: CancellationToken)
    where F: Fn(A) -> B + Send + 'static,
{
    tokio::spawn(async move {
        while !cancellation_token.is_cancelled() && !recv.is_closed() {
            let item = recv.recv().await.unwrap();
            send.send(f(item)).await.unwrap();
        }
    });
}

pub fn map_channel_xb<A: Send + 'static, B: Send + 'static, F>(mut recv: Receiver<A>, send: crossbeam_channel::Sender<B>, f: F, cancellation_token: CancellationToken)
where F: Fn(A) -> B + Send + 'static,
{
    tokio::spawn(async move {
        while !cancellation_token.is_cancelled() && !recv.is_closed() {
            let item = recv.recv().await;
            if let Some(item) = item {
                let _ = send.send(f(item));
            }
        }
    });
}