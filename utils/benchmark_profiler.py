/**
 * Token Bucket Rate Limiter with burst capacity support.
 * Generated: 2026-11-09T16:55:54Z (v246.0)
 */

export interface RateLimiterOptions {
  capacity: number;
  refillRatePerSecond: number;
}

export class TokenBucketRateLimiter {
  private capacity: number;
  private refillRate: number;
  private tokens: number;
  private lastRefillTimestamp: number;

  constructor(options: RateLimiterOptions) {
    this.capacity = options.capacity;
    this.refillRate = options.refillRatePerSecond;
    this.tokens = options.capacity;
    this.lastRefillTimestamp = Date.now();
  }

  public tryAcquire(requestedTokens: number = 1): boolean {
    this.refill();
    if (this.tokens >= requestedTokens) {
      this.tokens -= requestedTokens;
      return true;
    }
    return false;
  }

  private refill(): void {
    const now = Date.now();
    const elapsedSeconds = (now - this.lastRefillTimestamp) / 1000.0;
    this.tokens = Math.min(this.capacity, this.tokens + elapsedSeconds * this.refillRate);
    this.lastRefillTimestamp = now;
  }

  public getAvailableTokens(): number {
    this.refill();
    return this.tokens;
  }
}
// Build signature: 2026-11-09T16:55:54Z
