package mail

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"html/template"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/skip2/go-qrcode"
	gomail "gopkg.in/gomail.v2"
)

type CreateBookingResponse struct {
	ID          string
	EventID     string
	SeatNumbers []string
	CreatedAt   time.Time
}

func SendConfirmationMail(mailer *Mailer, resp CreateBookingResponse, event db.Event, toEmail string, includeQR bool) error {
	if mailer == nil {
		return fmt.Errorf("mailer is nil")
	}
	if toEmail == "" {
		return fmt.Errorf("recipient email is empty")
	}

	const AppURL = "https://app.overbookr.com"

	// prepare event pieces
	eventName := strings.TrimSpace(event.Name)
	venue := event.Venue.String
	startStr := event.StartTime.Time.Format("Mon, 02 Jan 2006 15:04 MST")

	// Subject
	subject := fmt.Sprintf("Your tickets for %s", eventName)

	// HTML template: use cid:qr_filename for the image src
	const tpl = `<!doctype html>
<html>
  <body style="margin:0;padding:0;background:#f4f6fb;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Arial;">
    <center style="width:100%;background:#f4f6fb;padding:28px 12px;">
      <table role="presentation" width="680" cellpadding="0" cellspacing="0" border="0" style="max-width:680px;width:100%;background:#ffffff;border-radius:12px;overflow:hidden;box-shadow:0 8px 30px rgba(15,23,42,0.06);">
        <tr>
          <td style="padding:18px 20px;background:linear-gradient(90deg,#0f172a,#0f3b91);color:#ffffff;">
            <table role="presentation" width="100%"><tr>
              <td style="vertical-align:middle;">
                <div style="font-size:18px;font-weight:700;line-height:1;">{{ .EventName }}</div>
                <div style="font-size:13px;opacity:0.9;margin-top:6px;">{{ .Venue }}</div>
              </td>
              <td align="right" style="vertical-align:middle;"><div style="font-size:12px;color:rgba(255,255,255,0.95);font-weight:600;">Overbookr</div></td>
            </tr></table>
          </td>
        </tr>

        <!-- Ticket -->
        <tr>
          <td style="padding:18px 20px;">
            <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:separate;border-radius:10px;overflow:hidden;">
              <tr>
                <td valign="top" width="64%" style="background:#ffffff;padding:18px;border:1px solid #eef2f7;border-right:none;">
                  <div style="font-size:11px;color:#6b7280;margin-bottom:6px;">Booking</div>
                  <div style="font-size:18px;font-weight:700;color:#0f172a;margin-bottom:12px;">{{ .EventName }}</div>

                  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;">
                    <tr>
                      <td style="vertical-align:top;font-size:13px;color:#374151;padding-right:12px;width:60%;">
                        <div style="font-weight:600;margin-bottom:6px;">When</div>
                        <div style="margin-bottom:10px;">{{ .StartTime }}</div>

                        <div style="font-weight:600;margin-bottom:6px;">Where</div>
                        <div style="margin-bottom:10px;">{{ .Venue }}</div>
                      </td>

                      <td style="vertical-align:top;font-size:13px;color:#374151;width:40%;">
                        <div style="font-weight:600;margin-bottom:6px;">Seats</div>
                        <div style="margin-bottom:10px;">
                          {{ range .SeatNumbers }}
                            <span style="display:inline-block;margin:4px 6px 4px 0;padding:6px 10px;border-radius:999px;font-weight:700;font-size:13px;background:#eef2ff;color:#0f3b91;">{{ . }}</span>
                          {{ end }}
                        </div>

                        <div style="margin-top:8px;">
                          <a href="{{ .BookingURL }}" style="display:inline-block;padding:8px 12px;font-weight:700;font-size:13px;text-decoration:none;border-radius:8px;background:#0f3b91;color:#ffffff;">View Booking</a>
                        </div>
                      </td>
                    </tr>
                  </table>
                </td>

                <td valign="top" width="1%" style="background:#ffffff;padding:0 8px;">
                  <div style="height:100%;width:1px;border-right:2px dashed #e6e9ef;"></div>
                </td>

                <td valign="top" width="35%" style="background:#fafbff;padding:18px;border:1px solid #eef2f7;border-left:none;text-align:center;">
                  <!-- embed via cid -->
                  <img src="cid:{{ .QRFilename }}" alt="Ticket QR" width="130" height="130" style="display:block;margin:0 auto 12px auto;border-radius:8px;"/>

                  <div style="font-size:12px;color:#6b7280;margin-bottom:6px;">Reference</div>
                  <div style="font-weight:700;color:#0f172a;margin-bottom:10px;">{{ .BookingID }}</div>

                  <div style="font-size:12px;color:#6b7280;margin-bottom:6px;">Issued</div>
                  <div style="font-size:13px;color:#374151;font-weight:600;margin-bottom:12px;">{{ .BookedOn }}</div>

                  <div style="margin-top:8px;">
                    <div style="height:12px;width:80%;margin:0 auto;background-image:linear-gradient(90deg,#0f172a 20%,transparent 20%);background-size:8px 12px;"></div>
                  </div>

                  <div style="font-size:12px;color:#9ca3af;line-height:1.3;padding-top:8px;">Show this booking reference at the gate.</div>
                </td>
              </tr>
            </table>
          </td>
        </tr>

        <tr>
          <td style="padding:16px 20px;background:#ffffff;border-top:1px solid #f1f5f9;">
            <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;">
              <tr>
                <td style="font-size:13px;color:#6b7280;">Keep this email as proof of booking. For changes or cancellations visit your booking page.</td>
                <td align="right" style="font-size:12px;color:#9ca3af;">Made with ❤️ — support@overbookr.com</td>
              </tr>
            </table>
          </td>
        </tr>
      </table>
    </center>
  </body>
</html>`

	// ---- generate QR PNG to temp file if requested ----
	qrFilename := ""    // basename used for cid
	var tempPath string // full path for embedding
	if includeQR {
		// make a safe filename, can use booking id prefix
		qrFilename = fmt.Sprintf("qr_%s.png", strings.ReplaceAll(resp.ID, "-", "")) // no dashes
		// generate PNG bytes
		png, err := qrcode.Encode(resp.ID, qrcode.Medium, 256)
		if err != nil {
			// Log or fallback: don't crash — you can continue without QR or return error
			// return fmt.Errorf("failed to generate qr: %w", err)
			qrFilename = "" // no qr
		} else {
			// write to temp file (system temp dir)
			tmpFile, err := ioutil.TempFile("", qrFilename)
			if err != nil {
				qrFilename = ""
			} else {
				tempPath = tmpFile.Name()
				if _, err := tmpFile.Write(png); err != nil {
					_ = tmpFile.Close()
					_ = os.Remove(tempPath)
					tempPath = ""
					qrFilename = ""
				} else {
					_ = tmpFile.Close()
					// Ensure basename for CID (some clients expect basename)
					qrFilename = filepath.Base(tempPath) // use actual temp name as CID token
				}
			}
		}
	}

	// prepare data for template
	data := struct {
		EventName   string
		Venue       string
		StartTime   string
		SeatNumbers []string
		SeatsCount  int
		BookingID   string
		BookedOn    string
		BookingURL  string
		QRFilename  string // used in cid:...
	}{
		EventName:   eventName,
		Venue:       venue,
		StartTime:   startStr,
		SeatNumbers: resp.SeatNumbers,
		SeatsCount:  len(resp.SeatNumbers),
		BookingID:   resp.ID,
		BookedOn:    resp.CreatedAt.Format("Mon, 02 Jan 2006 15:04 MST"),
		BookingURL:  fmt.Sprintf("%s/bookings/%s", AppURL, resp.ID),
		QRFilename:  qrFilename,
	}

	t, err := template.New("confirmation").Parse(tpl)
	if err != nil {
		// cleanup temp file if created
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		return fmt.Errorf("failed to parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		return fmt.Errorf("failed to execute template: %w", err)
	}
	htmlBody := buf.String()

	// Build message with gomail directly so we can Embed
	msg := gomail.NewMessage()
	from := "Overbookr <noreply@overbookr.com>"
	msg.SetHeader("From", from)
	msg.SetHeader("To", toEmail)
	msg.SetHeader("Subject", subject)
	msg.SetBody("text/html", htmlBody)

	// If we have a tempPath, embed it. gomail's Embed uses the file content and sets a content-id.
	if tempPath != "" {
		// Use Embed which sets Content-ID based on filename; our template uses cid:<basename>
		msg.Embed(tempPath)
	}

	// send using mailer config
	d := gomail.NewDialer(mailer.Host, mailer.Port, mailer.Username, mailer.Password)
	if mailer.InsecureSkipVerify {
		d.TLSConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if err := d.DialAndSend(msg); err != nil {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		// try plain fallback as before
		plain := buildPlainTextConfirmationWithEvent(resp, eventName, venue, time.Time{}, AppURL)
		_ = mailer.Send(from, []string{toEmail}, subject, plain, false)
		return fmt.Errorf("failed to send confirmation email: %w", err)
	}

	// cleanup the temp file (safe to remove after send)
	if tempPath != "" {
		_ = os.Remove(tempPath)
	}

	return nil
}

// helper that builds a small plain-text version of the confirmation (for fallback)
func buildPlainTextConfirmationWithEvent(resp CreateBookingResponse, eventName, venue string, start time.Time, appURL string) string {
	seats := "none"
	if len(resp.SeatNumbers) > 0 {
		seats = strings.Join(resp.SeatNumbers, ", ")
	}
	startStr := "TBD"
	if !start.IsZero() {
		startStr = start.Format("Mon, 02 Jan 2006 15:04 MST")
	}
	return fmt.Sprintf(
		"Booking confirmed!\n\nEvent: %s\nVenue: %s\nStarts: %s\n\nBooking ID: %s\nSeats: %s\nBooked on: %s\n\nView your booking: %s/bookings/%s\n\nThanks — OverBookr",
		eventName,
		venue,
		startStr,
		resp.ID,
		seats,
		resp.CreatedAt.Format("Mon, 02 Jan 2006 15:04 MST"),
		appURL,
		resp.ID,
	)
}
