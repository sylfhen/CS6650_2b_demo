"""HW5 load test for the Product API.

Request mix (realistic e-commerce: mostly browsing):
  85%  GET  /products/{id}          existing product   -> 200
   5%  GET  /products/{id}          missing product    -> 404 (correct, so success)
  10%  POST /products/{id}/details  valid update       -> 204

Environment variables:
  WAIT_MODE=think|none   think = 1-2 s between requests (default); none = back to back
  LOAD_SHAPE=stress      step ramp 50 -> 1200 users, 60 s per step
  SEED_PRODUCTS=1000     must match the server's seed count

Run one client type at a time by naming it: ProductFastUser or ProductHttpUser.
"""
import os
import random

from locust import FastHttpUser, HttpUser, LoadTestShape, between, constant

SEEDED = int(os.getenv("SEED_PRODUCTS", "1000"))
WAIT = constant(0) if os.getenv("WAIT_MODE") == "none" else between(1, 2)


def get_existing(user):
    pid = random.randint(1, SEEDED)
    user.client.get(f"/products/{pid}", name="GET /products/[id]")


def get_missing(user):
    pid = SEEDED + random.randint(1, 1_000_000)
    with user.client.get(f"/products/{pid}", name="GET /products/[id] (missing)",
                         catch_response=True) as resp:
        if resp.status_code == 404:
            resp.success()
        else:
            resp.failure(f"expected 404, got {resp.status_code}")


def post_details(user):
    pid = random.randint(1, SEEDED)
    user.client.post(f"/products/{pid}/details", name="POST /products/[id]/details", json={
        "product_id": pid,
        "sku": f"LOAD-{pid:06d}",
        "manufacturer": "Locust Load Co",
        "category_id": random.randint(1, 50),
        "weight": random.randint(0, 5000),
        "some_other_id": random.randint(1, 1000),
    })


TASKS = {get_existing: 85, get_missing: 5, post_details: 10}


class ProductHttpUser(HttpUser):
    """Uses the python-requests client (simpler, more CPU per request)."""
    wait_time = WAIT
    tasks = TASKS


class ProductFastUser(FastHttpUser):
    """Uses geventhttpclient (leaner, less CPU per request)."""
    wait_time = WAIT
    tasks = TASKS


if os.getenv("LOAD_SHAPE") == "stress":
    class StepStress(LoadTestShape):
        """Hold each user count for 60 s to find where the server stops keeping up."""
        steps = [50, 100, 200, 400, 800, 1200]
        step_seconds = 60
        spawn_rate = 50

        def tick(self):
            step = int(self.get_run_time() // self.step_seconds)
            if step >= len(self.steps):
                return None
            return self.steps[step], self.spawn_rate
