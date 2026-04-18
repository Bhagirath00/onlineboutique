#!/usr/bin/python
#
# Copyright 2018 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from locust import HttpUser, task, between
import random

products = [
    '0PUK6V6EV0', '1YMWWN1N4O', '2ZYFJ3GM2N', '66VCHSJNUP',
    '6E92ZMYYFZ', '9SIQT8TOJO', 'L9ECAV7KIM', 'LS4PSXUNUM', 'OLJCESPC7Z'
]

class OnlineBoutiqueUser(HttpUser):
    wait_time = between(1, 5)

    @task(10)
    def index(self):
        self.client.get("/")

    @task(5)
    def set_currency(self):
        currencies = ['EUR', 'USD', 'JPY', 'CAD']
        self.client.post("/setCurrency", {'currency_code': random.choice(currencies)})

    @task(20)
    def browse_product(self):
        self.client.get(f"/product/{random.choice(products)}")

    @task(10)
    def view_cart(self):
        self.client.get("/cart")

    @task(5)
    def add_to_cart(self):
        product_id = random.choice(products)
        self.client.post("/cart", {
            'product_id': product_id,
            'quantity': random.randint(1, 5)
        })

    @task(2)
    def checkout(self):
        # First add to cart to simulate real flow
        product_id = random.choice(products)
        self.client.post("/cart", {'product_id': product_id, 'quantity': 1})
        
        self.client.post("/cart/checkout", {
            'email': 'researcher@nexus.io',
            'street_address': 'EKS Cluster Node 2',
            'zip_code': '94101',
            'city': 'San Francisco',
            'state': 'CA',
            'country': 'US',
            'credit_card_number': '4242-4242-4242-4242',
            'credit_card_expiration_month': '12',
            'credit_card_expiration_year': '2025',
            'credit_card_cvv': '123'
        })
