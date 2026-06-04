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

	cloudflare "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/cloudflare/cloudflare-go/v7/zones"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
)

func newCloudflareClient(token string) *cloudflare.Client {
	return cloudflare.NewClient(option.WithAPIToken(token))
}

func cloudflareZoneIDByName(ctx context.Context, cloudflareAPI *cloudflare.Client, zoneName string) (string, error) {
	pager := cloudflareAPI.Zones.ListAutoPaging(ctx, zones.ZoneListParams{
		Name:    cloudflare.String(zoneName),
		PerPage: cloudflare.Float(50),
	})
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

func getCloudflareDNSRecord(ctx context.Context, cloudflareAPI *cloudflare.Client, zoneID, recordID string) (dns.RecordResponse, error) {
	record, err := cloudflareAPI.DNS.Records.Get(ctx, recordID, dns.RecordGetParams{
		ZoneID: cloudflare.String(zoneID),
	})
	if err != nil {
		return dns.RecordResponse{}, err
	}
	return *record, nil
}

func listCloudflareDNSRecords(ctx context.Context, cloudflareAPI *cloudflare.Client, zoneID string, params dns.RecordListParams) ([]dns.RecordResponse, error) {
	params.ZoneID = cloudflare.String(zoneID)
	if !params.PerPage.Present {
		params.PerPage = cloudflare.Float(1000)
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

func createCloudflareDNSRecord(ctx context.Context, cloudflareAPI *cloudflare.Client, zoneID string, desiredRecord cloudflareoperatoriov1.DNSRecordSpec) (dns.RecordResponse, error) {
	body, err := newCloudflareDNSRecordBody(desiredRecord)
	if err != nil {
		return dns.RecordResponse{}, err
	}

	record, err := cloudflareAPI.DNS.Records.New(ctx, dns.RecordNewParams{
		ZoneID: cloudflare.String(zoneID),
		Body:   body,
	})
	if err != nil {
		return dns.RecordResponse{}, err
	}
	return *record, nil
}

func editCloudflareDNSRecord(ctx context.Context, cloudflareAPI *cloudflare.Client, zoneID, recordID string, desiredRecord cloudflareoperatoriov1.DNSRecordSpec) error {
	body, err := editCloudflareDNSRecordBody(desiredRecord)
	if err != nil {
		return err
	}

	_, err = cloudflareAPI.DNS.Records.Edit(ctx, recordID, dns.RecordEditParams{
		ZoneID: cloudflare.String(zoneID),
		Body:   body,
	})
	return err
}

func deleteCloudflareDNSRecord(ctx context.Context, cloudflareAPI *cloudflare.Client, zoneID, recordID string) error {
	if recordID == "" {
		return nil
	}
	_, err := cloudflareAPI.DNS.Records.Delete(ctx, recordID, dns.RecordDeleteParams{
		ZoneID: cloudflare.String(zoneID),
	})
	return err
}

func newCloudflareDNSRecordBody(desiredRecord cloudflareoperatoriov1.DNSRecordSpec) (dns.RecordNewParamsBody, error) {
	data, err := cloudflareDNSRecordData(desiredRecord)
	if err != nil {
		return dns.RecordNewParamsBody{}, err
	}

	body := dns.RecordNewParamsBody{
		Name:    cloudflare.String(desiredRecord.Name),
		TTL:     cloudflare.F(dns.TTL(normalizedTTL(desiredRecord.TTL))),
		Type:    cloudflare.F(dns.RecordNewParamsBodyType(desiredRecord.Type)),
		Proxied: cloudflare.Bool(proxiedEnabled(desiredRecord.Proxied)),
		Comment: cloudflare.String(desiredRecord.Comment),
	}
	if desiredRecord.Content != "" || data == nil {
		body.Content = cloudflare.String(desiredRecord.Content)
	}
	if desiredRecord.Priority != nil {
		body.Priority = cloudflare.Float(float64(*desiredRecord.Priority))
	}
	if data != nil {
		body.Data = cloudflare.F[interface{}](data)
	}
	return body, nil
}

func editCloudflareDNSRecordBody(desiredRecord cloudflareoperatoriov1.DNSRecordSpec) (dns.RecordEditParamsBody, error) {
	data, err := cloudflareDNSRecordData(desiredRecord)
	if err != nil {
		return dns.RecordEditParamsBody{}, err
	}

	body := dns.RecordEditParamsBody{
		Name:    cloudflare.String(desiredRecord.Name),
		TTL:     cloudflare.F(dns.TTL(normalizedTTL(desiredRecord.TTL))),
		Type:    cloudflare.F(dns.RecordEditParamsBodyType(desiredRecord.Type)),
		Proxied: cloudflare.Bool(proxiedEnabled(desiredRecord.Proxied)),
		Comment: cloudflare.String(desiredRecord.Comment),
	}
	if desiredRecord.Content != "" || data == nil {
		body.Content = cloudflare.String(desiredRecord.Content)
	}
	if desiredRecord.Priority != nil {
		body.Priority = cloudflare.Float(float64(*desiredRecord.Priority))
	}
	if data != nil {
		body.Data = cloudflare.F[interface{}](data)
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
