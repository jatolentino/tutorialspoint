package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/jatolentino/tutorialspoint/api/web"
	"github.com/jatolentino/tutorialspoint/api/weberr"
	"github.com/jatolentino/tutorialspoint/config"
	"github.com/jatolentino/tutorialspoint/core/cart"
	"github.com/jatolentino/tutorialspoint/core/claims"
	"github.com/jatolentino/tutorialspoint/core/course"
	"github.com/jatolentino/tutorialspoint/database"
	"github.com/jatolentino/tutorialspoint/validate"
	"github.com/stripe/stripe-go/v74"
	stripecl "github.com/stripe/stripe-go/v74/client"
	"github.com/stripe/stripe-go/v74/webhook"

	"github.com/plutov/paypal/v4"
)

// checkout retrieves the latest details of the courses in the cart.
func checkout(ctx context.Context, db *sqlx.DB, userID string) ([]course.Course, error) {
	items, err := cart.FetchItems(ctx, db, userID)
	if err != nil {
		return nil, fmt.Errorf("fetching cart items: %w", err)
	}

	courses := make([]course.Course, 0, len(items))
	for _, it := range items {
		c, err := course.Fetch(ctx, db, it.CourseID)
		if err != nil {
			return nil, fmt.Errorf("fetching course[%s]: %w", it.CourseID, err)
		}

		courses = append(courses, c)
	}

	return courses, nil
}

// prepare creates the order and its items in the database,
// binding the order to the passed providerID.
func prepare(ctx context.Context, db *sqlx.DB, userID string, providerID string, courses []course.Course) error {
	err := database.Transaction(db, func(tx sqlx.ExtContext) error {
		now := time.Now().UTC()
		ord := Order{
			ID:         validate.GenerateID(),
			UserID:     userID,
			ProviderID: providerID,
			Status:     Pending,
			CreatedAt:  now,
			UpdatedAt:  now,
		}

		if err := Create(ctx, tx, ord); err != nil {
			return fmt.Errorf("creating order: %w", err)
		}

		for _, c := range courses {
			it := Item{
				OrderID:   ord.ID,
				CourseID:  c.ID,
				Price:     c.Price,
				CreatedAt: now,
			}

			if err := CreateItem(ctx, tx, it); err != nil {
				return fmt.Errorf("creating item: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("creating the order bound to payment[%s] for user[%s]: %w", providerID, userID, err)
	}
	return nil
}

func fulfill(ctx context.Context, db *sqlx.DB, providerID string) error {
	ord, err := FetchByProviderID(ctx, db, providerID)
	if err != nil {
		return fmt.Errorf("fetching the order bound to payment[%s]: %w", providerID, err)
	}

	err = database.Transaction(db, func(tx sqlx.ExtContext) error {
		up := StatusUp{
			ID:        ord.ID,
			Status:    Success,
			UpdatedAt: time.Now().UTC(),
		}

		if err = UpdateStatus(ctx, tx, up); err != nil {
			return fmt.Errorf("updating status: %w", err)
		}

		// Finally flush the cart as a last step.
		if err = cart.Delete(ctx, tx, ord.UserID); err != nil {
			return fmt.Errorf("flushing cart: %w", err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("fulfilling the order[%s] bound to payment[%s]: %w", ord.ID, providerID, err)
	}
	return nil
}

// HandlePaypalCheckout starts the purchase flow with paypal.
func HandlePaypalCheckout(db *sqlx.DB, pp *paypal.Client) web.Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		clm, err := claims.Get(ctx)
		if err != nil {
			return weberr.NotAuthorized(errors.New("user not authenticated"))
		}

		courses, err := checkout(ctx, db, clm.UserID)
		if err != nil {
			return fmt.Errorf("fetching details of cart items: %w", err)
		}

		if len(courses) == 0 {
			err := errors.New("no items to checkout")
			return weberr.NewError(err, err.Error(), http.StatusUnprocessableEntity)
		}

		var tot int
		items := make([]paypal.Item, 0, len(courses))
		for _, c := range courses {
			items = append(items, paypal.Item{
				Quantity:    "1",
				Name:        c.Name,
				Description: c.Description,

				UnitAmount: &paypal.Money{
					Currency: "USD",
					Value:    strconv.Itoa(c.Price),
				},
			})

			tot += c.Price
		}

		units := []paypal.PurchaseUnitRequest{{
			Items: items,

			Amount: &paypal.PurchaseUnitAmount{
				Currency: "USD",
				Value:    strconv.Itoa(tot),

				Breakdown: &paypal.PurchaseUnitAmountBreakdown{ItemTotal: &paypal.Money{
					Currency: "USD",
					Value:    strconv.Itoa(tot),
				}},
			},
		}}

		// TODO: Extract these params from the configuration.
		app := &paypal.ApplicationContext{
			// ReturnURL: "/success.html",
			// CancelURL: "/canceled.html",
		}

		ord, err := pp.CreateOrder(ctx, "CAPTURE", units, nil, app)
		if err != nil {
			return fmt.Errorf("creating paypal order: %w", err)
		}

		if err := prepare(ctx, db, clm.UserID, ord.ID, courses); err != nil {
			return fmt.Errorf("creating the order on the database: %w", err)
		}

		return web.Respond(ctx, w, ord, http.StatusOK)
	}
}

// HandlePaypalCapture checks if the user's purchase has been
// successfully completed. After the capture, the money of the user
// will be transferred to our paypal account.
func HandlePaypalCapture(db *sqlx.DB, pp *paypal.Client) web.Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		providerID := web.Param(r, "id")

		resp, err := pp.CaptureOrder(ctx, providerID, paypal.CaptureOrderRequest{})
		if err != nil {
			return fmt.Errorf("capturing paypal order[%s]: %w", providerID, err)
		}

		if resp.Status != "COMPLETED" {
			return fmt.Errorf("captured order[%s] with status[%s] different from 'COMPLETED'", providerID, resp.Status)
		}

		// WARNING: This is a critical error. The user has payed but he won't have the bought courses.
		// Take this issue in mind, manual recovery is needed at the moment.
		//
		// Putting an alarm here is a good compromise; so we don't need to use an external service
		// like redis or google pub-sub to enqueue the fulfillment request.
		// Then if this issue happens regularly we're going to solve it in a proper way.
		if err := fulfill(ctx, db, providerID); err != nil {
			// Try to refund the capture.
			// pp.RefundCapture(ctx, providerID, paypal.RefundCaptureRequest{})

			// Alternative: Use webhooks even for paypal capture.
			// https://developer.paypal.com/api/rest/webhooks/
			// PAYMENT.CAPTURE.COMPLETED https://developer.paypal.com/beta/apm-beta/additional-information/subscribe-to-webhooks/
			// However, this is in BETA...
			//
			// We could also try order webhooks but it says: `Orders webhooks are for use by Partners only`

			return fmt.Errorf("the order was payed but its fulfillment failed: %w", err)
		}

		return web.Respond(ctx, w, nil, http.StatusNoContent)
	}
}

// HandleStripeCheckout starts the purchase flow with stripe.
func HandleStripeCheckout(db *sqlx.DB, strp *stripecl.API, cfg config.Stripe) web.Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		clm, err := claims.Get(ctx)
		if err != nil {
			return weberr.NotAuthorized(errors.New("user not authenticated"))
		}

		courses, err := checkout(ctx, db, clm.UserID)
		if err != nil {
			return fmt.Errorf("fetching details of cart items: %w", err)
		}

		if len(courses) == 0 {
			err := errors.New("no items to checkout")
			return weberr.NewError(err, err.Error(), http.StatusUnprocessableEntity)
		}

		li := make([]*stripe.CheckoutSessionLineItemParams, 0, len(courses))
		for _, c := range courses {
			li = append(li, &stripe.CheckoutSessionLineItemParams{
				Quantity: stripe.Int64(1),

				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency:    stripe.String("usd"),
					TaxBehavior: stripe.String("inclusive"),
					UnitAmount:  stripe.Int64(int64(c.Price) * 100),

					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name:        stripe.String(c.Name),
						Description: stripe.String(c.Description),
					},
				},
			})
		}

		params := &stripe.CheckoutSessionParams{
			SuccessURL: stripe.String(cfg.SuccessURL),
			CancelURL:  stripe.String(cfg.CancelURL),
			Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
			LineItems:  li,
		}

		// Create a new stripe checkout with the courses to be bought.
		s, err := strp.CheckoutSessions.New(params)
		if err != nil {
			return fmt.Errorf("creating stripe session: %w", err)
		}

		if err := prepare(ctx, db, clm.UserID, s.ID, courses); err != nil {
			return fmt.Errorf("creating the order on the database: %w", err)
		}

		return web.Respond(ctx, w, s.URL, http.StatusOK)
	}
}

// HandleStripeCapture completes the user's purchase.
// Stripe webhooks must be configured to call this endpoint when a
// checkout is completed.
//
// TODO: Remember to disable async payments.
// https://stripe.com/docs/payments/checkout/fulfill-orders#delayed-notification .
// TODO: rename in HandleStripeWebhooks.
func HandleStripeCapture(db *sqlx.DB, cfg config.Stripe) web.Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		b, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return weberr.BadRequest(fmt.Errorf("cannot read the request body: %w", err))
		}

		sig := r.Header.Get("Stripe-Signature")
		if sig == "" {
			return weberr.BadRequest(errors.New("received stripe event is not signed"))
		}

		event, err := webhook.ConstructEvent(b, sig, cfg.WebhookSecret)
		if err != nil {
			return weberr.BadRequest(fmt.Errorf("cannot construct stripe event: %w", err))
		}

		// Filter all the events but the checkout completion one.
		if event.Type != "checkout.session.completed" {
			return web.Respond(ctx, w, nil, http.StatusNoContent)
		}

		var session stripe.CheckoutSession
		if err = json.Unmarshal(event.Data.Raw, &session); err != nil {
			return weberr.BadRequest(fmt.Errorf("unable to decode stripe event: %w", err))
		}

		// Filter out checkouts that are not for one-time payments.
		if session.Mode != stripe.CheckoutSessionModePayment {
			return web.Respond(ctx, w, nil, http.StatusNoContent)
		}

		if err := fulfill(ctx, db, session.ID); err != nil {
			return fmt.Errorf("the order was payed but its fulfillment failed: %w", err)
		}

		return web.Respond(ctx, w, nil, http.StatusNoContent)
	}
}
