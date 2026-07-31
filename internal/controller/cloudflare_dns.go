/*
Copyright 2025 containeroo

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/cloudflare/cloudflare-go/v7/zones"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
)

type cloudflareClient struct {
	DNS   *dns.DNSService
	Zones *zones.ZoneService
}

func newCloudflareClient(token string) *cloudflareClient {
	opts := []option.RequestOption{option.WithAPIToken(token)}
	return &cloudflareClient{
		DNS:   dns.NewDNSService(opts...),
		Zones: zones.NewZoneService(opts...),
	}
}

func cloudflareZoneIDByName(ctx context.Context, cloudflareAPI *cloudflareClient, zoneName string) (string, error) {
	params := zones.ZoneListParams{}
	params.Name.Value = zoneName
	params.Name.Present = true
	params.PerPage.Value = 50
	params.PerPage.Present = true
	pager := cloudflareAPI.Zones.ListAutoPaging(ctx, params)
	for pager.Next() {
		zone := pager.Current()
		if zone.Name == zoneName {
			return zone.ID, nil
		}
	}
	if err := pager.Err(); err != nil {
		return "", err
	}

	return "", errors.New("zone could not be found")
}

func getCloudflareDNSRecord(ctx context.Context, cloudflareAPI *cloudflareClient, zoneID, recordID string) (dns.RecordResponse, error) {
	params := dns.RecordGetParams{}
	params.ZoneID.Value = zoneID
	params.ZoneID.Present = true
	record, err := cloudflareAPI.DNS.Records.Get(ctx, recordID, params)
	if err != nil {
		return dns.RecordResponse{}, err
	}
	return *record, nil
}

func listCloudflareDNSRecords(ctx context.Context, cloudflareAPI *cloudflareClient, zoneID string, params dns.RecordListParams) ([]dns.RecordResponse, error) {
	params.ZoneID.Value = zoneID
	params.ZoneID.Present = true
	if !params.PerPage.Present {
		params.PerPage.Value = 1000
		params.PerPage.Present = true
	}

	var records []dns.RecordResponse
	pager := cloudflareAPI.DNS.Records.ListAutoPaging(ctx, params)
	for pager.Next() {
		records = append(records, pager.Current())
	}
	if err := pager.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func createCloudflareDNSRecord(ctx context.Context, cloudflareAPI *cloudflareClient, zoneID string, desiredRecord cloudflareoperatoriov1.DNSRecordSpec) (dns.RecordResponse, error) {
	body, err := newCloudflareDNSRecordBody(desiredRecord)
	if err != nil {
		return dns.RecordResponse{}, err
	}

	params := dns.RecordNewParams{Body: body}
	params.ZoneID.Value = zoneID
	params.ZoneID.Present = true
	record, err := cloudflareAPI.DNS.Records.New(ctx, params)
	if err != nil {
		return dns.RecordResponse{}, err
	}
	return *record, nil
}

func editCloudflareDNSRecord(ctx context.Context, cloudflareAPI *cloudflareClient, zoneID, recordID string, desiredRecord cloudflareoperatoriov1.DNSRecordSpec) error {
	body, err := editCloudflareDNSRecordBody(desiredRecord)
	if err != nil {
		return err
	}

	params := dns.RecordEditParams{Body: body}
	params.ZoneID.Value = zoneID
	params.ZoneID.Present = true
	_, err = cloudflareAPI.DNS.Records.Edit(ctx, recordID, params)
	return err
}

func deleteCloudflareDNSRecord(ctx context.Context, cloudflareAPI *cloudflareClient, zoneID, recordID string) error {
	if recordID == "" {
		return nil
	}
	params := dns.RecordDeleteParams{}
	params.ZoneID.Value = zoneID
	params.ZoneID.Present = true
	_, err := cloudflareAPI.DNS.Records.Delete(ctx, recordID, params)
	return err
}

func newCloudflareDNSRecordBody(desiredRecord cloudflareoperatoriov1.DNSRecordSpec) (dns.RecordNewParamsBody, error) {
	data, err := cloudflareDNSRecordData(desiredRecord)
	if err != nil {
		return dns.RecordNewParamsBody{}, err
	}

	body := dns.RecordNewParamsBody{}
	body.Name.Value, body.Name.Present = desiredRecord.Name, true
	body.TTL.Value, body.TTL.Present = dns.TTL(normalizedTTL(desiredRecord.TTL)), true
	body.Type.Value, body.Type.Present = dns.RecordNewParamsBodyType(desiredRecord.Type), true
	body.Proxied.Value, body.Proxied.Present = proxiedEnabled(desiredRecord.Proxied), true
	body.Comment.Value, body.Comment.Present = desiredRecord.Comment, true
	if desiredRecord.Content != "" || data == nil {
		body.Content.Value, body.Content.Present = desiredRecord.Content, true
	}
	if desiredRecord.Priority != nil {
		body.Priority.Value, body.Priority.Present = float64(*desiredRecord.Priority), true
	}
	if data != nil {
		body.Data.Value, body.Data.Present = data, true
	}
	return body, nil
}

func editCloudflareDNSRecordBody(desiredRecord cloudflareoperatoriov1.DNSRecordSpec) (dns.RecordEditParamsBody, error) {
	data, err := cloudflareDNSRecordData(desiredRecord)
	if err != nil {
		return dns.RecordEditParamsBody{}, err
	}

	body := dns.RecordEditParamsBody{}
	body.Name.Value, body.Name.Present = desiredRecord.Name, true
	body.TTL.Value, body.TTL.Present = dns.TTL(normalizedTTL(desiredRecord.TTL)), true
	body.Type.Value, body.Type.Present = dns.RecordEditParamsBodyType(desiredRecord.Type), true
	body.Proxied.Value, body.Proxied.Present = proxiedEnabled(desiredRecord.Proxied), true
	body.Comment.Value, body.Comment.Present = desiredRecord.Comment, true
	if desiredRecord.Content != "" || data == nil {
		body.Content.Value, body.Content.Present = desiredRecord.Content, true
	}
	if desiredRecord.Priority != nil {
		body.Priority.Value, body.Priority.Present = float64(*desiredRecord.Priority), true
	}
	if data != nil {
		body.Data.Value, body.Data.Present = data, true
	}
	return body, nil
}

func cloudflareDNSRecordData(desiredRecord cloudflareoperatoriov1.DNSRecordSpec) (any, error) {
	if desiredRecord.Data == nil {
		return nil, nil
	}

	var data any
	if err := json.Unmarshal(desiredRecord.Data.Raw, &data); err != nil {
		return nil, fmt.Errorf("failed to parse DNS record data: %w", err)
	}
	return data, nil
}

func normalizedTTL(ttl int) float64 {
	if ttl == 0 {
		return 1
	}
	return float64(ttl)
}

func isCloudflareDNSRecordNotFound(err error) bool {
	var apiErr *dns.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}
