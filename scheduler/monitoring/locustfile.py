import random
from locust import HttpUser, task, between

class OnlineBoutiqueUser(HttpUser):
    wait_time = between(1, 2.5)

    @task(5)
    def browse_products(self):
        # Home page usually lists products
        self.client.get("/")

    @task(2)
    def view_product(self):
        product_ids = [
            'OLJCESPC7Z', '66VCHSJNUP', '0PUK6V6EV0', 
            '9SIQT8TOJO', '1YMWWN1N4O', 'L9ECAV7KIM'
        ]
        self.client.get(f"/product/{random.choice(product_ids)}")

    @task(1)
    def add_to_cart(self):
        product_ids = [
            'OLJCESPC7Z', '66VCHSJNUP', '0PUK6V6EV0'
        ]
        self.client.post("/cart", data={
            "product_id": random.choice(product_ids),
            "quantity": random.randint(1, 5)
        })

    @task(1)
    def view_cart(self):
        self.client.get("/cart")
